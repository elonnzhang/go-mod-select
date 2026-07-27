package controller

import (
	"fmt"
	"path/filepath"

	"go-mod-select/pkg/finder"
	"go-mod-select/pkg/parser"
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

	switch {
	case cfg.DryRun:
		return runDryMode(cfg, goMod, projectRoot)
	case cfg.AutoSelect:
		return runAutoMode(cfg, goMod, projectRoot)
	}

	// The TUI performs the update itself on its updating screen. It used to
	// also hand the selection back for the caller to apply, which ran every
	// `go get` a second time.
	return ui.TUI(cfg.TargetGoVersion, goMod, projectRoot)
}

// printPlan reports the analysis outcome shared by dry-run and auto modes.
func printPlan(cfg *Config, goMod *parser.GoMod, infos []*finder.ModuleInfo) {
	fmt.Printf("[INFO] Module:            %s\n", goMod.Module)
	fmt.Printf("[INFO] Current Go:        %s\n", goMod.GoVersion)
	fmt.Printf("[INFO] Target Go:         %s\n", cfg.TargetGoVersion)
	fmt.Printf("[INFO] Direct deps:       %d\n", parser.CountDirectDeps(goMod.Requires))
	fmt.Printf("[INFO] Indirect deps:     %d\n", parser.CountIndirectDeps(goMod.Requires))

	if len(infos) == 0 {
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
func runDryMode(cfg *Config, goMod *parser.GoMod, projectRoot string) error {
	fmt.Printf("[INFO] Analyzing dependencies for compatibility with Go %s...\n\n", cfg.TargetGoVersion)

	infos := ui.FindCompatibleVersionsParallel(goMod.Requires, cfg.TargetGoVersion, projectRoot)
	printPlan(cfg, goMod, infos)

	fmt.Println("\n[INFO] Dry run - no changes were made.")
	return nil
}

// runAutoMode selects every compatible update and applies it without prompting.
func runAutoMode(cfg *Config, goMod *parser.GoMod, projectRoot string) error {
	fmt.Printf("[INFO] Auto mode - analyzing dependencies for Go %s...\n\n", cfg.TargetGoVersion)

	infos := ui.FindCompatibleVersionsParallel(goMod.Requires, cfg.TargetGoVersion, projectRoot)
	printPlan(cfg, goMod, infos)

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

	upd := updater.NewUpdater(goMod)
	if err := upd.UpdateAll(cfg.TargetGoVersion, apply, cfg.Verbose); err != nil {
		return fmt.Errorf("update failed (go.mod and go.sum restored): %w", err)
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
