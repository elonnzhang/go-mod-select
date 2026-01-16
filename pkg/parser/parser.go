package parser

import (
	"fmt"
	"os"

	"golang.org/x/mod/modfile"
)

// GoMod represents a parsed go.mod file
type GoMod struct {
	FilePath  string
	Content   []byte // Original content for rollback
	GoVersion string
	Module    string
	Requires  []Require
	Replace   []Replace
}

// Require represents a module dependency
type Require struct {
	Path     string
	Version  string
	Indirect bool
}

// Replace is a single replace statement.
type Replace struct {
	OldPath    string
	OldVersion string
	NewPath    string
	NewVersion string
}

// ParseGoMod parses a go.mod file and returns a GoMod structure
func ParseGoMod(path string) (*GoMod, error) {
	// Read file
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read go.mod: %w", err)
	}

	// Parse using modfile package
	file, err := modfile.Parse(path, content, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to parse go.mod: %w", err)
	}

	// Extract Go version
	goVersion := ""
	if file.Go != nil {
		goVersion = file.Go.Version
	}

	// Extract module name
	moduleName := ""
	if file.Module != nil {
		moduleName = file.Module.Mod.Path
	}

	// Build GoMod structure
	goMod := &GoMod{
		FilePath:  path,
		Content:   content,
		GoVersion: goVersion,
		Module:    moduleName,
		Requires:  make([]Require, 0),
	}

	// Extract requires
	if file.Require != nil {
		for _, req := range file.Require {
			goMod.Requires = append(goMod.Requires, Require{
				Path:     req.Mod.Path,
				Version:  req.Mod.Version,
				Indirect: req.Indirect,
			})
		}
	}

	// Extract replace
	if file.Replace != nil {
		for _, req := range file.Replace {
			goMod.Replace = append(goMod.Replace, Replace{
				OldPath:    req.Old.Path,
				OldVersion: req.Old.Version,
				NewPath:    req.New.Path,
				NewVersion: req.New.Version,
			})
		}
	}

	return goMod, nil
}

// UpdateGoVersion updates the Go version in the go.mod file
func (gm *GoMod) UpdateGoVersion(version string) error {
	content, err := os.ReadFile(gm.FilePath)
	if err != nil {
		return fmt.Errorf("failed to read go.mod: %w", err)
	}

	file, err := modfile.Parse(gm.FilePath, content, nil)
	if err != nil {
		return fmt.Errorf("failed to parse go.mod: %w", err)
	}

	// Update Go version
	if err := file.AddGoStmt(version); err != nil {
		return fmt.Errorf("failed to update Go version: %w", err)
	}

	// Format and write back
	formatted, err := file.Format()
	if err != nil {
		return fmt.Errorf("failed to format go.mod: %w", err)
	}

	if err := os.WriteFile(gm.FilePath, formatted, 0o644); err != nil {
		return fmt.Errorf("failed to write go.mod: %w", err)
	}

	gm.GoVersion = version
	return nil
}

// UpdateRequire updates a single dependency version
func (gm *GoMod) UpdateRequire(path, version string) error {
	content, err := os.ReadFile(gm.FilePath)
	if err != nil {
		return fmt.Errorf("failed to read go.mod: %w", err)
	}

	file, err := modfile.Parse(gm.FilePath, content, nil)
	if err != nil {
		return fmt.Errorf("failed to parse go.mod: %w", err)
	}

	// Update the require statement
	if err := file.AddRequire(path, version); err != nil {
		return fmt.Errorf("failed to update require: %w", err)
	}

	// Format and write back
	formatted, err := file.Format()
	if err != nil {
		return fmt.Errorf("failed to format go.mod: %w", err)
	}

	if err := os.WriteFile(gm.FilePath, formatted, 0o644); err != nil {
		return fmt.Errorf("failed to write go.mod: %w", err)
	}

	return nil
}

// Save writes the current state to the go.mod file
func (gm *GoMod) Save() error {
	return os.WriteFile(gm.FilePath, gm.Content, 0o644)
}

// Backup returns the original content for rollback
func (gm *GoMod) Backup() []byte {
	return gm.Content
}

// Restore restores the go.mod file from backup
func (gm *GoMod) Restore(backup []byte) error {
	return os.WriteFile(gm.FilePath, backup, 0o644)
}

func CountDirectDeps(requires []Require) int {
	count := 0
	for _, req := range requires {
		if !req.Indirect {
			count++
		}
	}
	return count
}

func CountIndirectDeps(requires []Require) int {
	count := 0
	for _, req := range requires {
		if req.Indirect {
			count++
		}
	}
	return count
}
