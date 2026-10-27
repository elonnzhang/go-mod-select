package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/elonnzhang/go-mod-select/pkg/controller"
	"github.com/elonnzhang/go-mod-select/pkg/finder"
	"github.com/elonnzhang/go-mod-select/pkg/logger"

	"github.com/spf13/cobra"
)

var (
	goVersion      string
	legacyVersion  string
	dryRun         bool
	verbose        bool
	modFile        string
	workDir        string
	autoSelect     bool
	allowDowngrade bool
	verify         bool
	pre            bool
	maxUpdate      string
)

var rootCmd = &cobra.Command{
	Use:   "go-mod-select [version]",
	Short: "Interactive Go module version selector",
	Long: `go-mod-select is an interactive CLI tool for managing Go module version compatibility.
It helps you select and update a Go version in go.mod while ensuring all dependencies
remain compatible with the target version.

Without --dir or --gomod, the module root is located by walking up from the current
directory, the way the go tool does, so running inside a subpackage works.

Analysis is read-only: version queries run in a temporary sandbox module and never
rewrite your go.mod or go.sum.

Usage examples:
  go-mod-select 1.25                          # Walk up to the module root
  go-mod-select 1.25 --dir /path/to/project   # Use go.mod in specified directory
  go-mod-select 1.25 --gomod /path/to/go.mod  # Use specified go.mod file
  go-mod-select 1.25 --dry-run                # Print the plan, change nothing
  go-mod-select 1.25 --auto                   # Apply everything without prompting
  go-mod-select 1.25 --auto --verify          # ...and roll back if the project stops building
  go-mod-select 1.25 --max-update minor       # Never cross a major version
  go-mod-select 1.25 --pre                    # Consider prerelease tags too
  go-mod-select 1.25 --verbose                # Pass through go get output`,
	Args:          cobra.MaximumNArgs(1),
	RunE:          run,
	SilenceErrors: true, // Execute prints the error once itself
	SilenceUsage:  true, // a runtime failure is not a usage problem
}

func init() {
	rootCmd.Flags().StringVar(&goVersion, "go", "", "Target Go version (e.g., 1.23, 1.24.1); same as the positional argument")

	// -v used to take the target version, and --version with it. --version now
	// prints this tool's version, as everywhere else; -v keeps working, loudly,
	// so scripts written for the old spelling are not broken without a word.
	rootCmd.Flags().StringVarP(&legacyVersion, "legacy-version", "v", "", "")
	for _, err := range []error{
		rootCmd.Flags().MarkHidden("legacy-version"),
		rootCmd.Flags().MarkShorthandDeprecated("legacy-version", "use --go or the positional argument; --version now prints the tool's version"),
	} {
		if err != nil {
			panic(err) // a typo in a flag name above: caught the first time the binary runs
		}
	}
	rootCmd.Version = toolVersion()
	rootCmd.SetVersionTemplate("go-mod-select {{.Version}}\n")
	rootCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show available updates without applying them")
	rootCmd.Flags().BoolVar(&verbose, "verbose", false, "Enable verbose output")
	rootCmd.Flags().StringVar(&modFile, "gomod", "", "Path to go.mod file")
	rootCmd.Flags().StringVar(&workDir, "dir", "", "Working directory (will look for go.mod in this directory)")
	rootCmd.Flags().BoolVar(&autoSelect, "auto", false, "Automatically select and apply all compatible updates")
	rootCmd.Flags().BoolVar(&allowDowngrade, "allow-downgrade", false,
		"In --auto mode, also apply versions older than the current one when the current version needs a newer Go")
	rootCmd.Flags().BoolVar(&verify, "verify", false,
		"After updating, run go build ./... and roll everything back if the project no longer builds (skipped if it did not build before)")
	rootCmd.Flags().BoolVar(&pre, "pre", false,
		"Also propose prerelease tags (v1.2.0-rc1); by default they are skipped unless the module is already on one")
	rootCmd.Flags().StringVar(&maxUpdate, "max-update", "major",
		"Largest jump to propose from the current version: patch, minor or major")
	rootCmd.MarkFlagsMutuallyExclusive("gomod", "dir")
	rootCmd.MarkFlagsMutuallyExclusive("dry-run", "verify")
	rootCmd.MarkFlagsMutuallyExclusive("dry-run", "auto")
}

// Execute enters the main execution flow of the CLI tool.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

// resolveTargetVersion picks the target Go version from the positional argument,
// --go, or the legacy -v, and insists they agree if more than one is given.
func resolveTargetVersion(goFlag, legacyFlag string, args []string) (string, error) {
	clean := func(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "go") }

	var given []string
	if len(args) > 0 {
		given = append(given, clean(args[0]))
	}
	for _, v := range []string{goFlag, legacyFlag} {
		if v != "" {
			given = append(given, clean(v))
		}
	}

	if len(given) == 0 {
		return "", fmt.Errorf("target Go version is required\nUsage: go-mod-select <version> (e.g. go-mod-select 1.25)")
	}
	for _, v := range given[1:] {
		if v != given[0] {
			return "", fmt.Errorf("conflicting target Go versions %q and %q: give it once", given[0], v)
		}
	}
	return given[0], nil
}

func run(cmd *cobra.Command, args []string) error {
	// Logging is best-effort: a read-only home directory should not stop the
	// tool from doing its job.
	if err := logger.InitDefault(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: file logging disabled: %v\n", err)
	}
	logger.Log.Info("go-mod-select started")

	targetVersion, err := resolveTargetVersion(goVersion, legacyVersion, args)
	if err != nil {
		return err
	}

	// Validate against the same parser used for compatibility comparisons, so
	// what the CLI accepts and what the finder can compare cannot drift apart.
	if !finder.IsValidGoVersion(targetVersion) {
		logger.Log.Error("invalid Go version format", "version", targetVersion)
		return fmt.Errorf("invalid Go version %q (expected e.g. 1.24 or 1.24.1)", targetVersion)
	}

	limit, err := finder.ParseLimit(maxUpdate)
	if err != nil {
		return err
	}

	goModPath, err := resolveGoModPath(modFile, workDir)
	if err != nil {
		logger.Log.Error("failed to resolve go.mod path", "error", err)
		return err
	}

	logger.Log.Info("resolved go.mod path", "path", goModPath)

	info, err := os.Stat(goModPath)
	switch {
	case os.IsNotExist(err):
		logger.Log.Error("go.mod not found", "path", goModPath)
		return fmt.Errorf("no go.mod at %s\nRun from a module directory, or pass --dir/--gomod", goModPath)
	case err != nil:
		return fmt.Errorf("cannot read %s: %w", goModPath, err)
	case info.IsDir():
		return fmt.Errorf("%s is a directory, not a go.mod file", goModPath)
	}

	cfg := &controller.Config{
		TargetGoVersion: targetVersion,
		GoModPath:       goModPath,
		DryRun:          dryRun,
		Verbose:         verbose,
		AutoSelect:      autoSelect,
		AllowDowngrade:  allowDowngrade,
		Verify:          verify,
		Policy:          finder.Policy{AllowPrerelease: pre, MaxUpdate: limit},
	}

	logger.Log.Info("starting controller",
		"target", targetVersion, "gomod", goModPath, "dry_run", dryRun, "auto", autoSelect)

	return controller.Run(cfg)
}

// resolveGoModPath determines the go.mod path based on three modes:
//  1. --gomod specified: use the exact file path
//  2. --dir specified: look for go.mod in that directory
//  3. neither specified: walk up from the current directory to the module root,
//     the way the go tool itself does, so running in a subpackage works.
func resolveGoModPath(gomodFlag, dirFlag string) (string, error) {
	if gomodFlag != "" {
		absPath, err := filepath.Abs(gomodFlag)
		if err != nil {
			return "", fmt.Errorf("invalid --gomod path: %w", err)
		}

		// The file must actually be named go.mod. An alternate name would be
		// edited in place while `go get` and `go mod tidy` — which resolve the
		// module root from the directory, and cannot be redirected by -modfile
		// without an existing go.mod — operated on the directory's real go.mod
		// instead. Rollback would then restore a go.sum belonging to a
		// different module.
		if base := filepath.Base(absPath); base != "go.mod" {
			return "", fmt.Errorf("--gomod must point at a file named go.mod, got %q\n"+
				"The go tool resolves the module from the containing directory, so an alternate\n"+
				"filename would be edited while go get operated on %s",
				base, filepath.Join(filepath.Dir(absPath), "go.mod"))
		}

		return absPath, nil
	}

	if dirFlag != "" {
		absDir, err := filepath.Abs(dirFlag)
		if err != nil {
			return "", fmt.Errorf("invalid --dir path: %w", err)
		}
		return filepath.Join(absDir, "go.mod"), nil
	}

	currentDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %w", err)
	}
	return findModuleRoot(currentDir), nil
}

// findModuleRoot walks up from dir looking for a go.mod, returning the path to
// the nearest one. If none is found it returns dir/go.mod so the caller reports
// a "not found" error against the directory the user actually ran in.
func findModuleRoot(dir string) string {
	for d := dir; ; {
		candidate := filepath.Join(d, "go.mod")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}

		parent := filepath.Dir(d)
		if parent == d {
			return filepath.Join(dir, "go.mod")
		}
		d = parent
	}
}
