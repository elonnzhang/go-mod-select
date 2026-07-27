package updater

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"go-mod-select/pkg/finder"
	"go-mod-select/pkg/logger"
	"go-mod-select/pkg/parser"
)

// ProgressCallback is called during update to report progress
type ProgressCallback func(current, total int, module, status string)

// TidyLabel is the module name reported to the progress callback for the
// `go mod tidy` step. Callers match on it to render that step differently.
const TidyLabel = "go mod tidy"

// Updater handles updating Go modules and the go.mod file
type Updater struct {
	goMod   *parser.GoMod
	workDir string

	// Snapshot of both module files taken before the first mutation. `go get`
	// rewrites go.sum as well as go.mod, so restoring only go.mod would leave
	// the module in a state that neither builds nor matches the original.
	goModBackup []byte
	goSumBackup []byte
	// goSumExisted distinguishes "go.sum was empty" from "go.sum did not
	// exist", so rollback can delete a file we caused to be created.
	goSumExisted bool
}

// NewUpdater snapshots the module files as they are on disk right now.
//
// The snapshot must come from disk, not from goMod.Content: that field is
// captured once at startup and never refreshed, so a second update round would
// roll back to the program-start contents and silently undo the first round's
// applied upgrades.
func NewUpdater(goMod *parser.GoMod) *Updater {
	workDir := filepath.Dir(goMod.FilePath)

	u := &Updater{
		goMod:   goMod,
		workDir: workDir,
	}

	if mod, err := os.ReadFile(goMod.FilePath); err == nil {
		u.goModBackup = mod
	} else {
		// Fall back to the parsed content so rollback still has something to
		// write; a go.mod we cannot read is a bigger problem than a stale one.
		logger.Log.Warn("could not re-read go.mod for backup, using parsed content",
			"path", goMod.FilePath, "error", err.Error())
		u.goModBackup = goMod.Backup()
	}

	if sum, err := os.ReadFile(u.goSumPath()); err == nil {
		u.goSumBackup = sum
		u.goSumExisted = true
	}

	return u
}

func (u *Updater) goSumPath() string {
	return filepath.Join(u.workDir, "go.sum")
}

// UpdateGoVersion updates the Go version in go.mod
func (u *Updater) UpdateGoVersion(version string) error {
	if err := u.goMod.UpdateGoVersion(version); err != nil {
		return fmt.Errorf("failed to update Go version: %w", err)
	}
	return nil
}

// UpdateModulesWithProgress updates the selected modules with progress reporting
func (u *Updater) UpdateModulesWithProgress(modules []*finder.ModuleInfo, verbose bool, progressCb ProgressCallback) error {
	total := len(modules)

	for i, mod := range modules {
		if progressCb != nil {
			progressCb(i+1, total, mod.Path, "updating")
		}

		if err := u.updateSingleModule(mod, verbose); err != nil {
			if progressCb != nil {
				progressCb(i+1, total, mod.Path, "error")
			}
			return fmt.Errorf("failed to update %s: %w", mod.Path, err)
		}

		if progressCb != nil {
			progressCb(i+1, total, mod.Path, "success")
		}
	}

	return nil
}

// updateSingleModule updates a single module using go get
func (u *Updater) updateSingleModule(mod *finder.ModuleInfo, verbose bool) error {
	moduleSpec := fmt.Sprintf("%s@%s", mod.Path, mod.LatestCompatible)
	return u.runGo(verbose, "get", moduleSpec)
}

// runGo runs a go subcommand in the target module directory, returning stderr
// as part of the error so failures are actionable in the TUI log.
func (u *Updater) runGo(verbose bool, args ...string) error {
	cmd := exec.Command("go", args...)
	cmd.Dir = u.workDir

	var stderr bytes.Buffer
	if verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	} else {
		cmd.Stderr = &stderr
	}

	if err := cmd.Run(); err != nil {
		logger.Log.Error("go command failed", "args", args, "error", err.Error(), "stderr", stderr.String())
		return fmt.Errorf("go %s: %w\n%s", args[0], err, bytes.TrimSpace(stderr.Bytes()))
	}
	return nil
}

// UpdateModules updates the selected modules to their target versions.
func (u *Updater) UpdateModules(modules []*finder.ModuleInfo, verbose bool) error {
	if len(modules) == 0 {
		return nil
	}

	fmt.Printf("\n[INFO] Updating %d module(s)...\n", len(modules))

	for i, mod := range modules {
		fmt.Printf("[%d/%d] Updating %s: %s → %s...\n",
			i+1, len(modules), mod.Path, mod.CurrentVersion, mod.LatestCompatible)

		if err := u.updateSingleModule(mod, verbose); err != nil {
			return fmt.Errorf("failed to update %s: %w", mod.Path, err)
		}

		fmt.Printf("[INFO] ✓ %s updated successfully\n", mod.Path)
	}

	return nil
}

// TidyWithProgress runs go mod tidy with progress callback
func (u *Updater) TidyWithProgress(verbose bool, progressCb ProgressCallback) error {
	if progressCb != nil {
		progressCb(0, 0, TidyLabel, "tidying")
	}

	if err := u.runGo(verbose, "mod", "tidy"); err != nil {
		if progressCb != nil {
			progressCb(0, 0, TidyLabel, "error")
		}
		return fmt.Errorf("go mod tidy failed: %w", err)
	}

	if progressCb != nil {
		progressCb(0, 0, TidyLabel, "success")
	}
	return nil
}

// Tidy runs go mod tidy to clean up the go.mod and go.sum files
func (u *Updater) Tidy(verbose bool) error {
	fmt.Println("\n[INFO] Running go mod tidy...")

	if err := u.runGo(verbose, "mod", "tidy"); err != nil {
		return fmt.Errorf("go mod tidy failed: %w", err)
	}

	fmt.Println("[INFO] ✓ go mod tidy completed")
	return nil
}

// VerifyGoVersion re-reads go.mod and reports the go directive actually in
// effect after the update.
//
// `go mod tidy` raises the directive on its own when a remaining dependency
// requires a newer toolchain, so the value the user asked for is not
// necessarily the value that ends up on disk. Silently accepting that turns
// "downgrade to 1.17" into "still on 1.25" with no indication anything went
// differently.
func (u *Updater) VerifyGoVersion(requested string) (actual string, matched bool, err error) {
	gm, err := parser.ParseGoMod(u.goMod.FilePath)
	if err != nil {
		return "", false, fmt.Errorf("cannot re-read go.mod to verify: %w", err)
	}

	actual = gm.GoVersion
	// Compare at language-version granularity: asking for 1.24 and getting
	// 1.24.0 is the go tool normalizing, not a failure to apply.
	return actual, finder.SameGoLanguageVersion(actual, requested), nil
}

// Rollback restores go.mod and go.sum to their pre-update contents.
//
// Both files are attempted even if the first restore fails, so a broken go.mod
// does not leave a stale go.sum behind.
func (u *Updater) Rollback() error {
	var errs []error

	if err := u.goMod.Restore(u.goModBackup); err != nil {
		errs = append(errs, fmt.Errorf("restore go.mod: %w", err))
	}

	switch {
	case u.goSumExisted:
		mode := os.FileMode(0o644)
		if info, err := os.Stat(u.goSumPath()); err == nil {
			mode = info.Mode().Perm()
		}
		if err := os.WriteFile(u.goSumPath(), u.goSumBackup, mode); err != nil {
			errs = append(errs, fmt.Errorf("restore go.sum: %w", err))
		}
	default:
		// go.sum did not exist before; remove one the update created.
		if err := os.Remove(u.goSumPath()); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove generated go.sum: %w", err))
		}
	}

	if len(errs) > 0 {
		logger.Log.Error("rollback incomplete", "errors", fmt.Sprint(errs))
		return fmt.Errorf("rollback failed: %w", errors.Join(errs...))
	}

	logger.Log.Info("rolled back go.mod and go.sum")
	return nil
}

// rollbackOn wraps a failure with rollback, surfacing a rollback error as
// additional context rather than swallowing either one.
func (u *Updater) rollbackOn(cause error) error {
	if rbErr := u.Rollback(); rbErr != nil {
		return fmt.Errorf("%w (%v)", cause, rbErr)
	}
	return cause
}

// currentGoDirective reads the go directive from disk.
//
// The in-memory parser.GoMod.GoVersion is not trustworthy across rounds: a
// rolled-back UpdateGoVersion leaves the field set to the version it failed to
// keep, so comparing against it would skip writing the directive on the next
// attempt.
func (u *Updater) currentGoDirective() string {
	gm, err := parser.ParseGoMod(u.goMod.FilePath)
	if err != nil {
		logger.Log.Warn("could not read current go directive", "error", err.Error())
		return ""
	}
	return gm.GoVersion
}

// applyGoVersion writes the go directive unless it already matches on disk.
func (u *Updater) applyGoVersion(targetGoVersion string) error {
	if finder.SameGoLanguageVersion(u.currentGoDirective(), targetGoVersion) {
		return nil
	}
	return u.UpdateGoVersion(targetGoVersion)
}

// UpdateAllWithProgress performs a complete update with progress reporting
func (u *Updater) UpdateAllWithProgress(targetGoVersion string, modules []*finder.ModuleInfo, verbose bool, progressCb ProgressCallback) error {
	if err := u.applyGoVersion(targetGoVersion); err != nil {
		return u.rollbackOn(err)
	}

	if err := u.UpdateModulesWithProgress(modules, verbose, progressCb); err != nil {
		return u.rollbackOn(err)
	}

	if err := u.TidyWithProgress(verbose, progressCb); err != nil {
		return u.rollbackOn(err)
	}

	return nil
}

// UpdateAll performs a complete update: Go version + modules + tidy
func (u *Updater) UpdateAll(targetGoVersion string, modules []*finder.ModuleInfo, verbose bool) error {
	if err := u.applyGoVersion(targetGoVersion); err != nil {
		return u.rollbackOn(err)
	}

	if err := u.UpdateModules(modules, verbose); err != nil {
		return u.rollbackOn(err)
	}

	if err := u.Tidy(verbose); err != nil {
		return u.rollbackOn(err)
	}

	return nil
}
