package controller

import (
	"fmt"
	"path/filepath"

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
}

// Run executes the main logic of go-mod-select
func Run(cfg *Config) error {
	// Step 1: Parse go.mod
	goMod, err := parser.ParseGoMod(cfg.GoModPath)
	if err != nil {
		return fmt.Errorf("failed to parse go.mod: %w", err)
	}

	// Get project root (directory containing go.mod)
	projectRoot := filepath.Dir(cfg.GoModPath)

	// For dry-run mode, show info and exit
	if cfg.DryRun {
		return runDryMode(cfg, goMod)
	}

	// For auto-select mode, use old flow
	if cfg.AutoSelect {
		return runAutoMode(cfg, goMod)
	}

	// Run full TUI mode
	selectedModules, err := ui.TUI(cfg.TargetGoVersion, goMod, projectRoot)
	if err != nil {
		return fmt.Errorf("TUI failed: %w", err)
	}

	if len(selectedModules) == 0 {
		return nil
	}

	// Perform updates
	upd := updater.NewUpdater(goMod)
	if err := upd.UpdateAll(cfg.TargetGoVersion, selectedModules, cfg.Verbose); err != nil {
		fmt.Printf("\n[ERROR] Update failed: %v\n", err)
		return err
	}

	return nil
}

// runDryMode shows information without making changes
func runDryMode(cfg *Config, goMod *parser.GoMod) error {
	fmt.Printf("[INFO] Current Go version: %s\n", goMod.GoVersion)
	fmt.Printf("[INFO] Target Go version: %s\n", cfg.TargetGoVersion)
	fmt.Printf("[INFO] Module: %s\n", goMod.Module)
	fmt.Printf("[INFO] Dependencies: %d\n", len(goMod.Requires))

	fmt.Printf("\n[INFO] Analyzing dependencies for compatibility with Go %s...\n", cfg.TargetGoVersion)

	moduleInfos := ui.FindCompatibleVersionsParallel(goMod.Requires, cfg.TargetGoVersion, cfg.Verbose)

	if len(moduleInfos) == 0 {
		fmt.Println("\n[INFO] All dependencies are already at their latest compatible versions.")
		return nil
	}

	fmt.Printf("\n[INFO] Found %d module(s) with available updates:\n", len(moduleInfos))
	for _, info := range moduleInfos {
		fmt.Printf("  - %s: %s → %s (%s)\n",
			info.Path,
			info.CurrentVersion,
			info.LatestCompatible,
			info.UpdateType)
	}

	fmt.Println("\n[INFO] Dry run mode - no changes will be made.")
	return nil
}

// runAutoMode runs in automatic selection mode
func runAutoMode(cfg *Config, goMod *parser.GoMod) error {
	fmt.Printf("[INFO] Current Go version: %s\n", goMod.GoVersion)
	fmt.Printf("[INFO] Target Go version: %s\n", cfg.TargetGoVersion)
	fmt.Printf("[INFO] Auto-select mode - selecting all compatible updates.\n")

	fmt.Printf("\n[INFO] Analyzing dependencies...\n")

	moduleInfos := ui.FindCompatibleVersionsParallel(goMod.Requires, cfg.TargetGoVersion, cfg.Verbose)

	if len(moduleInfos) == 0 {
		fmt.Println("\n[INFO] All dependencies are already at their latest compatible versions.")
		return nil
	}

	fmt.Printf("\n[INFO] Found %d module(s) with available updates\n", len(moduleInfos))

	upd := updater.NewUpdater(goMod)
	if err := upd.UpdateAll(cfg.TargetGoVersion, moduleInfos, cfg.Verbose); err != nil {
		fmt.Printf("\n[ERROR] Update failed: %v\n", err)
		return err
	}

	fmt.Println("\n✓ Update completed successfully!")
	return nil
}
