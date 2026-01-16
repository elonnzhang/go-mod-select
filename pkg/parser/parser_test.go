package parser

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseGoMod(t *testing.T) {
	// Create a temporary go.mod file
	tmpDir := t.TempDir()
	goModPath := filepath.Join(tmpDir, "go.mod")

	goModContent := `module test-module

go 1.19

replace vitess.io/vitess => github.com/vitessio/vitess v0.0.0-20191113025808-0629f0da20ab


require (
	github.com/beego/beego/v2 v2.0.2
	github.com/dustin/go-humanize v1.0.0
	github.com/go-sql-driver/mysql v1.7.0 // indirect
)
`

	err := os.WriteFile(goModPath, []byte(goModContent), 0o644)
	if err != nil {
		t.Fatalf("Failed to create test go.mod: %v", err)
	}

	// Parse the go.mod
	goMod, err := ParseGoMod(goModPath)
	if err != nil {
		t.Fatalf("ParseGoMod failed: %v", err)
	}

	// Verify Go version
	if goMod.GoVersion != "1.19" {
		t.Errorf("Expected Go version 1.19, got %s", goMod.GoVersion)
	}

	// Verify module name
	if goMod.Module != "test-module" {
		t.Errorf("Expected module name test-module, got %s", goMod.Module)
	}

	// Verify requires
	if len(goMod.Requires) != 3 {
		t.Errorf("Expected 3 requires, got %d", len(goMod.Requires))
	}

	// Verify replace
	if len(goMod.Replace) != 1 {
		t.Errorf("Expected 1 replace , got %d", len(goMod.Replace))
	}

	// Check first require
	if goMod.Requires[0].Path != "github.com/beego/beego/v2" {
		t.Errorf("Expected first require to be github.com/beego/beego/v2, got %s", goMod.Requires[0].Path)
	}

	// Check indirect flag
	hasIndirect := false
	for _, req := range goMod.Requires {
		if req.Indirect {
			hasIndirect = true
			break
		}
	}
	if !hasIndirect {
		t.Error("Expected at least one indirect dependency")
	}
}

func TestUpdateGoVersion(t *testing.T) {
	// Create a temporary go.mod file
	tmpDir := t.TempDir()
	goModPath := filepath.Join(tmpDir, "go.mod")

	goModContent := `module test-module

go 1.19

require (
	github.com/beego/beego/v2 v2.0.2
)
`

	err := os.WriteFile(goModPath, []byte(goModContent), 0o644)
	if err != nil {
		t.Fatalf("Failed to create test go.mod: %v", err)
	}

	// Parse and update
	goMod, err := ParseGoMod(goModPath)
	if err != nil {
		t.Fatalf("ParseGoMod failed: %v", err)
	}

	err = goMod.UpdateGoVersion("1.25")
	if err != nil {
		t.Fatalf("UpdateGoVersion failed: %v", err)
	}

	// Re-parse and verify
	goMod2, err := ParseGoMod(goModPath)
	if err != nil {
		t.Fatalf("ParseGoMod failed after update: %v", err)
	}

	if goMod2.GoVersion != "1.25" {
		t.Errorf("Expected Go version 1.25, got %s", goMod2.GoVersion)
	}
}

func TestBackupAndRestore(t *testing.T) {
	// Create a temporary go.mod file
	tmpDir := t.TempDir()
	goModPath := filepath.Join(tmpDir, "go.mod")

	originalContent := `module test-module

go 1.19
`

	err := os.WriteFile(goModPath, []byte(originalContent), 0o644)
	if err != nil {
		t.Fatalf("Failed to create test go.mod: %v", err)
	}

	// Parse
	goMod, err := ParseGoMod(goModPath)
	if err != nil {
		t.Fatalf("ParseGoMod failed: %v", err)
	}

	// Backup
	backup := goMod.Backup()

	// Modify
	err = goMod.UpdateGoVersion("1.25")
	if err != nil {
		t.Fatalf("UpdateGoVersion failed: %v", err)
	}

	// Restore
	err = goMod.Restore(backup)
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	// Verify restoration
	content, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatalf("Failed to read restored go.mod: %v", err)
	}

	if string(content) != originalContent {
		t.Errorf("Content not restored correctly.\nExpected:\n%s\nGot:\n%s", originalContent, string(content))
	}
}
