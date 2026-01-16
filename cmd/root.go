package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"go-mod-select/pkg/controller"
	"go-mod-select/pkg/logger"

	"github.com/spf13/cobra"
)

var (
	version    string
	dryRun     bool
	verbose    bool
	modFile    string
	workDir    string
	autoSelect bool
)

var rootCmd = &cobra.Command{
	Use:   "go-mod-select [version]",
	Short: "Interactive Go module version selector",
	Long: `go-mod-select is an interactive CLI tool for managing Go module version compatibility.
It helps you select and update a Go version in go.mod while ensuring all dependencies
remain compatible with the target version.

Usage examples:
  go-mod-select 1.25                          # Use go.mod in current directory
  go-mod-select 1.25 -dir /path/to/project    # Use go.mod in specified directory
  go-mod-select 1.25 -gomod /path/to/go.mod   # Use specified go.mod file
  go-mod-select 1.25 --dry-run
  go-mod-select 1.25 --verbose`,
	Args: cobra.MaximumNArgs(1),
	RunE: run,
}

func init() {
	rootCmd.Flags().StringVarP(&version, "version", "v", "", "Target Go version (e.g., 1.23, 1.24.1)")
	rootCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show available updates without applying them")
	rootCmd.Flags().BoolVar(&verbose, "verbose", false, "Enable verbose output")
	rootCmd.Flags().StringVar(&modFile, "gomod", "", "Path to go.mod file")
	rootCmd.Flags().StringVar(&workDir, "dir", "", "Working directory (will look for go.mod in this directory)")
	rootCmd.Flags().BoolVar(&autoSelect, "auto", false, "Automatically select all compatible updates")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	// Initialize logger
	if err := logger.InitDefault(); err != nil {
		return fmt.Errorf("failed to initialize logger: %w", err)
	}
	logger.Log.Info("go-mod-select started")

	// Parse version from positional argument or flag
	targetVersion := version
	if len(args) > 0 {
		targetVersion = args[0]
	}

	if targetVersion == "" {
		return fmt.Errorf("target Go version is required\nUsage: go-mod-select <version> or go-mod-select -version <version>")
	}

	logger.Log.Info("target version", "version", targetVersion)

	// Validate Go version format
	if err := validateGoVersion(targetVersion); err != nil {
		logger.Log.Error("invalid Go version format", "error", err)
		return err
	}

	// Determine go.mod path based on three modes
	goModPath, err := resolveGoModPath(modFile, workDir)
	if err != nil {
		logger.Log.Error("failed to resolve go.mod path", "error", err)
		return err
	}

	logger.Log.Info("resolved go.mod path", "path", goModPath)

	// Check if go.mod exists
	if _, err := os.Stat(goModPath); os.IsNotExist(err) {
		logger.Log.Error("go.mod not found", "path", goModPath)
		return fmt.Errorf("go.mod not found at path: %s", goModPath)
	}

	// Import controller
	cfg := &controller.Config{
		TargetGoVersion: targetVersion,
		GoModPath:       goModPath,
		DryRun:          dryRun,
		Verbose:         verbose,
		AutoSelect:      autoSelect,
	}

	logger.Log.Info("starting controller", "config", cfg)

	return controller.Run(cfg)
}

// resolveGoModPath determines the go.mod path based on three modes:
// 1. -gomod specified: use the exact file path
// 2. -dir specified: look for go.mod in that directory
// 3. neither specified: look for go.mod in current directory
func resolveGoModPath(gomodFlag, dirFlag string) (string, error) {
	// Mode 1: -gomod specified (highest priority)
	if gomodFlag != "" {
		absPath, err := filepath.Abs(gomodFlag)
		if err != nil {
			return "", fmt.Errorf("invalid gomod path: %w", err)
		}
		return absPath, nil
	}

	// Mode 2: -dir specified
	if dirFlag != "" {
		absDir, err := filepath.Abs(dirFlag)
		if err != nil {
			return "", fmt.Errorf("invalid directory path: %w", err)
		}
		return filepath.Join(absDir, "go.mod"), nil
	}

	// Mode 3: current directory (default)
	currentDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %w", err)
	}
	return filepath.Join(currentDir, "go.mod"), nil
}

// validateGoVersion validates the Go version format
// Accepts formats like: 1.23, 1.24.1, 1.25.0
func validateGoVersion(ver string) error {
	// Go version pattern: 1.XX or 1.XX.Y
	pattern := `^1\.\d+(\.\d+)?$`
	matched, err := regexp.MatchString(pattern, ver)
	if err != nil {
		return fmt.Errorf("error validating version: %w", err)
	}
	if !matched {
		return fmt.Errorf("invalid Go version format: %s (expected format: 1.23 or 1.24.1)", ver)
	}
	return nil
}
