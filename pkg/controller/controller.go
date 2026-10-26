package controller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"go-mod-select/pkg/finder"
	"go-mod-select/pkg/parser"
	"go-mod-select/pkg/scan"
	"go-mod-select/pkg/ui"
	"go-mod-select/pkg/updater"
)

// Config holds the configuration for the controller
type Config struct {
	TargetGoVersion string
	GoModPath       string
	DryRun          bool
	Verbose         bool
	AutoSelect      bool
	// AllowDowngrade lets auto mode apply a version older than the current one
	// when the current version requires a newer Go than the target.
	AllowDowngrade bool
}

// Run executes the main logic of go-mod-select
func Run(cfg *Config) error {
	goMod, err := parser.ParseGoMod(cfg.GoModPath)
	if err != nil {
		return fmt.Errorf("failed to parse go.mod: %w", err)
	}

	// go commands must run inside the module being analysed, not the tool's cwd.
	projectRoot := filepath.Dir(cfg.GoModPath)

	if cfg.DryRun || cfg.AutoSelect {
		// Ctrl+C reaches `go` and this process alike. Left alone both die: the
		// analysis strands its scratch module, and an update leaves go.mod
		// half-rewritten. Catching the signal lets analysis clean up and the
		// updater interrupt the command in flight and roll back.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		defer stop()

		if cfg.DryRun {
			return runDryMode(ctx, cfg, goMod, projectRoot)
		}
		return runAutoMode(ctx, cfg, goMod, projectRoot)
	}

	// The TUI performs the update itself on its updating screen. It used to
	// also hand the selection back for the caller to apply, which ran every
	// `go get` a second time.
	return ui.TUI(cfg.TargetGoVersion, goMod, projectRoot)
}

// errInterrupted ends a command-line run the user cancelled.
var errInterrupted = errors.New("interrupted")

// analyze looks up the newest compatible version of every direct requirement.
// It returns the modules with a different version available, plus the lookups
// that failed: an error is not "already up to date", and a plan that silently
// omitted those modules would say it was.
func analyze(ctx context.Context, cfg *Config, goMod *parser.GoMod, projectRoot string) ([]*finder.ModuleInfo, []scan.Result, error) {
	f := finder.NewWithContext(ctx, projectRoot)
	defer f.Close()

	direct, _ := parser.SplitRequires(goMod.Requires)
	results := scan.Run(ctx, func(path, current string) (*finder.ModuleInfo, error) {
		return f.FindCompatibleVersions(path, current, cfg.TargetGoVersion)
	}, direct, nil)

	if ctx.Err() != nil {
		return nil, nil, errInterrupted
	}

	var failed []scan.Result
	for _, r := range results {
		if r.Err != nil {
			failed = append(failed, r)
		}
	}
	return scan.Updatable(results), failed, nil
}

// printFailures lists lookups that could not be completed.
func printFailures(failed []scan.Result) {
	if len(failed) == 0 {
		return
	}
	fmt.Printf("\n[WARN] %d module(s) could not be analyzed and are NOT covered by the plan above:\n", len(failed))
	for _, r := range failed {
		fmt.Printf("  - %s: %v\n", r.Pkg, r.Err)
	}
}

// updateFailure words a failed unattended update. It must not claim the files
// were restored when the rollback itself failed: the output may be all a CI log
// or an unattended user ever sees.
func updateFailure(err error) error {
	switch {
	case errors.Is(err, updater.ErrRollbackFailed):
		return fmt.Errorf("update failed and the rollback was incomplete; inspect go.mod and go.sum (e.g. git diff): %w", err)
	case errors.Is(err, updater.ErrCancelled):
		return errors.New("interrupted (go.mod and go.sum restored)")
	default:
		return fmt.Errorf("update failed (go.mod and go.sum restored): %w", err)
	}
}

// printPlan reports the analysis outcome shared by dry-run and auto modes.
func printPlan(cfg *Config, goMod *parser.GoMod, infos []*finder.ModuleInfo, unanalyzed int) {
	fmt.Printf("[INFO] Module:            %s\n", goMod.Module)
	fmt.Printf("[INFO] Current Go:        %s\n", goMod.GoVersion)
	fmt.Printf("[INFO] Target Go:         %s\n", cfg.TargetGoVersion)
	fmt.Printf("[INFO] Direct deps:       %d\n", parser.CountDirectDeps(goMod.Requires))
	fmt.Printf("[INFO] Indirect deps:     %d\n", parser.CountIndirectDeps(goMod.Requires))

	if len(infos) == 0 {
		if unanalyzed > 0 {
			fmt.Printf("\n[INFO] No updates found among the dependencies that could be analyzed.\n")
			return
		}
		fmt.Printf("\n[INFO] All direct dependencies are already at their newest Go %s-compatible version.\n",
			cfg.TargetGoVersion)
		return
	}

	fmt.Printf("\n[INFO] %d module(s) with a different compatible version:\n", len(infos))
	for _, info := range infos {
		arrow := "→"
		note := ""
		if info.Downgrade {
			// Surfacing this in the CLI matters more than in the TUI: there is
			// no chance to inspect the row before it is applied.
			arrow = "↓"
			note = "  (downgrade: current version needs a newer Go)"
		} else if info.BlockedNewer > 0 {
			note = fmt.Sprintf("  (%d newer version(s) need a later Go)", info.BlockedNewer)
		}
		fmt.Printf("  - %s: %s %s %s (%s)%s\n",
			info.Path, info.CurrentVersion, arrow, info.LatestCompatible, info.UpdateType, note)
	}
}

// runDryMode shows information without making changes
func runDryMode(ctx context.Context, cfg *Config, goMod *parser.GoMod, projectRoot string) error {
	fmt.Printf("[INFO] Analyzing dependencies for compatibility with Go %s...\n\n", cfg.TargetGoVersion)

	infos, failed, err := analyze(ctx, cfg, goMod, projectRoot)
	if err != nil {
		return err
	}
	printPlan(cfg, goMod, infos, len(failed))
	printFailures(failed)

	fmt.Println("\n[INFO] Dry run - no changes were made.")
	return nil
}

// runAutoMode selects every compatible update and applies it without prompting.
func runAutoMode(ctx context.Context, cfg *Config, goMod *parser.GoMod, projectRoot string) error {
	fmt.Printf("[INFO] Auto mode - analyzing dependencies for Go %s...\n\n", cfg.TargetGoVersion)

	infos, failed, err := analyze(ctx, cfg, goMod, projectRoot)
	if err != nil {
		return err
	}
	printPlan(cfg, goMod, infos, len(failed))
	printFailures(failed)

	// Downgrades are excluded from unattended runs. They are a legitimate
	// outcome when the pinned version needs a newer toolchain than the target,
	// but moving a dependency backwards is not what "apply all updates" means,
	// and nobody is watching to veto it.
	apply := make([]*finder.ModuleInfo, 0, len(infos))
	var skipped []*finder.ModuleInfo
	for _, info := range infos {
		if info.Downgrade && !cfg.AllowDowngrade {
			skipped = append(skipped, info)
			continue
		}
		apply = append(apply, info)
	}

	if len(skipped) > 0 {
		fmt.Printf("\n[WARN] Skipping %d downgrade(s) in auto mode (pass --allow-downgrade to include them):\n", len(skipped))
		for _, info := range skipped {
			fmt.Printf("  - %s: %s ↓ %s\n", info.Path, info.CurrentVersion, info.LatestCompatible)
		}
	}

	if len(apply) == 0 && cfg.TargetGoVersion == goMod.GoVersion {
		return nil
	}

	upd := updater.NewUpdater(goMod).WithContext(ctx)
	if err := upd.UpdateAll(cfg.TargetGoVersion, apply, cfg.Verbose); err != nil {
		return updateFailure(err)
	}

	// `go mod tidy` can raise the go directive past what was requested when a
	// remaining dependency needs a newer toolchain. Report that rather than
	// claiming the requested version was applied.
	actual, matched, err := upd.VerifyGoVersion(cfg.TargetGoVersion)
	switch {
	case err != nil:
		fmt.Printf("\n[WARN] could not verify the final go directive: %v\n", err)
	case !matched:
		fmt.Printf("\n[WARN] go.mod ended up on go %s, not the requested %s.\n", actual, cfg.TargetGoVersion)
		// Distinguish "tidy raised it" from "the write did not stick"; only the
		// first is a dependency problem.
		if finder.GoVersionSatisfied(cfg.TargetGoVersion, actual) {
			fmt.Println("       A remaining dependency requires the newer toolchain; run with --dry-run to see which.")
		} else {
			fmt.Println("       The directive is lower than requested, so the change may not have been applied.")
		}
		return nil
	}

	fmt.Println("\n✓ Update completed successfully!")
	return nil
}
