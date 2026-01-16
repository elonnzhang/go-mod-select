package updater

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"go-mod-select/pkg/finder"
	"go-mod-select/pkg/parser"
)

// ProgressCallback is called during update to report progress
type ProgressCallback func(current, total int, module, status string)

// Updater handles updating Go modules and the go.mod file
type Updater struct {
	goMod  *parser.GoMod
	backup []byte
}

// NewUpdater creates a new Updater instance
func NewUpdater(goMod *parser.GoMod) *Updater {
	return &Updater{
		goMod:  goMod,
		backup: goMod.Backup(),
	}
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
	if len(modules) == 0 {
		return nil
	}

	workDir := filepath.Dir(u.goMod.FilePath)
	total := len(modules)

	for i, mod := range modules {
		// Report starting update
		if progressCb != nil {
			progressCb(i+1, total, mod.Path, "updating")
		}

		if err := u.updateSingleModule(mod, workDir, verbose); err != nil {
			if progressCb != nil {
				progressCb(i+1, total, mod.Path, "error")
			}
			return fmt.Errorf("failed to update %s: %w", mod.Path, err)
		}

		// Report success
		if progressCb != nil {
			progressCb(i+1, total, mod.Path, "success")
		}
	}

	return nil
}

// updateSingleModule updates a single module using go get
func (u *Updater) updateSingleModule(mod *finder.ModuleInfo, workDir string, verbose bool) error {
	moduleSpec := fmt.Sprintf("%s@%s", mod.Path, mod.LatestCompatible)

	cmd := exec.Command("go", "get", moduleSpec)
	cmd.Dir = workDir

	if verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w\nOutput: %s", err, output)
	}

	return nil
}

// UpdateModules updates the selected modules to their target versions (for backward compatibility)
func (u *Updater) UpdateModules(modules []*finder.ModuleInfo, verbose bool) error {
	if len(modules) == 0 {
		return nil
	}

	fmt.Printf("\n[INFO] Updating %d module(s)...\n", len(modules))

	workDir := filepath.Dir(u.goMod.FilePath)

	for i, mod := range modules {
		fmt.Printf("[%d/%d] Updating %s: %s → %s...\n",
			i+1, len(modules),
			mod.Path,
			mod.CurrentVersion,
			mod.LatestCompatible)

		if err := u.updateSingleModule(mod, workDir, verbose); err != nil {
			return fmt.Errorf("failed to update %s: %w", mod.Path, err)
		}

		fmt.Printf("[INFO] ✓ %s updated successfully\n", mod.Path)
	}

	return nil
}

// TidyWithProgress runs go mod tidy with progress callback
func (u *Updater) TidyWithProgress(verbose bool, progressCb ProgressCallback) error {
	if progressCb != nil {
		progressCb(0, 0, "go mod tidy", "tidying")
	}

	workDir := filepath.Dir(u.goMod.FilePath)

	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = workDir

	if verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		if progressCb != nil {
			progressCb(0, 0, "go mod tidy", "error")
		}
		return fmt.Errorf("go mod tidy failed: %w\nOutput: %s", err, output)
	}

	if progressCb != nil {
		progressCb(0, 0, "go mod tidy", "success")
	}
	return nil
}

// Tidy runs go mod tidy to clean up the go.mod and go.sum files
func (u *Updater) Tidy(verbose bool) error {
	fmt.Println("\n[INFO] Running go mod tidy...")

	workDir := filepath.Dir(u.goMod.FilePath)

	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = workDir

	if verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go mod tidy failed: %w\nOutput: %s", err, output)
	}

	fmt.Println("[INFO] ✓ go mod tidy completed")
	return nil
}

// Rollback restores the go.mod file from backup
func (u *Updater) Rollback() error {
	if err := u.goMod.Restore(u.backup); err != nil {
		return fmt.Errorf("rollback failed: %w", err)
	}
	return nil
}

// UpdateAllWithProgress performs a complete update with progress reporting
func (u *Updater) UpdateAllWithProgress(targetGoVersion string, modules []*finder.ModuleInfo, verbose bool, progressCb ProgressCallback) error {
	// Update Go version
	if targetGoVersion != u.goMod.GoVersion {
		if err := u.UpdateGoVersion(targetGoVersion); err != nil {
			u.Rollback()
			return err
		}
	}

	// Update modules
	if err := u.UpdateModulesWithProgress(modules, verbose, progressCb); err != nil {
		u.Rollback()
		return err
	}

	// Run go mod tidy
	if err := u.TidyWithProgress(verbose, progressCb); err != nil {
		u.Rollback()
		return err
	}

	return nil
}

// UpdateAll performs a complete update: Go version + modules + tidy
func (u *Updater) UpdateAll(targetGoVersion string, modules []*finder.ModuleInfo, verbose bool) error {
	// Update Go version
	if targetGoVersion != u.goMod.GoVersion {
		if err := u.UpdateGoVersion(targetGoVersion); err != nil {
			u.Rollback()
			return err
		}
	}

	// Update modules
	if err := u.UpdateModules(modules, verbose); err != nil {
		u.Rollback()
		return err
	}

	// Run go mod tidy
	if err := u.Tidy(verbose); err != nil {
		u.Rollback()
		return err
	}

	return nil
}

// BackupGoModAndSum creates backups of go.mod and go.sum files
func BackupGoModAndSum(goModPath string) (goModBackup, goSumBackup []byte, err error) {
	// Backup go.mod
	goModBackup, err = os.ReadFile(goModPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to backup go.mod: %w", err)
	}

	// Backup go.sum if it exists
	goSumPath := filepath.Join(filepath.Dir(goModPath), "go.sum")
	if _, err := os.Stat(goSumPath); err == nil {
		goSumBackup, err = os.ReadFile(goSumPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to backup go.sum: %w", err)
		}
	}

	return goModBackup, goSumBackup, nil
}

// RestoreGoModAndSum restores go.mod and go.sum from backups
func RestoreGoModAndSum(goModPath string, goModBackup, goSumBackup []byte) error {
	// Restore go.mod
	if err := os.WriteFile(goModPath, goModBackup, 0o644); err != nil {
		return fmt.Errorf("failed to restore go.mod: %w", err)
	}

	// Restore go.sum if backup exists
	if goSumBackup != nil {
		goSumPath := filepath.Join(filepath.Dir(goModPath), "go.sum")
		if err := os.WriteFile(goSumPath, goSumBackup, 0o644); err != nil {
			return fmt.Errorf("failed to restore go.sum: %w", err)
		}
	}

	return nil
}
