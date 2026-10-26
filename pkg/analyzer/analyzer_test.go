package analyzer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindFilesUsingPackage(t *testing.T) {
	root := t.TempDir()

	files := map[string]string{
		"main.go":             "package main\n\nimport \"github.com/target/lib\"\n\nvar _ = lib.X\n",
		"sub/helper.go":       "package sub\n\nimport (\n\t\"fmt\"\n\t\"github.com/target/lib/inner\"\n)\n\nvar _ = fmt.Sprint\n",
		"sub/unrelated.go":    "package sub\n\nimport \"strings\"\n\nvar _ = strings.Title\n",
		"main_test.go":        "package main\n\nimport \"github.com/target/lib\"\n",
		"vendor/dep/vend.go":  "package dep\n\nimport \"github.com/target/lib\"\n",
		".hidden/hid.go":      "package hid\n\nimport \"github.com/target/lib\"\n",
		"testdata/fixture.go": "package testdata\n\nimport \"github.com/target/lib\"\n",
		"broken.go":           "package main\n\nthis is not go source at all",
		"prefixtrap/trap.go":  "package prefixtrap\n\nimport \"github.com/target/libextra\"\n",
		"node_modules/x/y.go": "package y\n\nimport \"github.com/target/lib\"\n",
	}

	for rel, content := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := FindFilesUsingPackage(root, "github.com/target/lib")
	if err != nil {
		t.Fatalf("FindFilesUsingPackage: %v", err)
	}

	want := []string{
		"main.go",
		"main_test.go", // tests are real usage sites worth showing
		filepath.Join("sub", "helper.go"),
	}

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %q, want %q (results should be sorted)", i, got[i], want[i])
		}
	}
}

func TestUsesPackageDoesNotMatchPathPrefix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte("package a\n\nimport \"github.com/target/libextra\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// "github.com/target/lib" must not match "github.com/target/libextra":
	// they are unrelated modules that happen to share a prefix.
	if usesPackage(path, "github.com/target/lib") {
		t.Error("libextra matched as lib")
	}
	if !usesPackage(path, "github.com/target/libextra") {
		t.Error("exact import not matched")
	}
}

func TestUsesPackageMatchesSubpackage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte("package a\n\nimport \"github.com/target/lib/deep/pkg\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !usesPackage(path, "github.com/target/lib") {
		t.Error("a subpackage import should count as using the module")
	}
}

func TestFindFilesUsingPackageOnMissingRoot(t *testing.T) {
	// A nonexistent root must not panic; an empty result is fine.
	got, _ := FindFilesUsingPackage(filepath.Join(t.TempDir(), "nope"), "github.com/x/y")
	if len(got) != 0 {
		t.Errorf("got %v, want no results", got)
	}
}

// writeTree creates files under root, making parent directories as needed.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestFindWhyPackageNeededReportsChain guards the bug where the command was
// invoked with -mod=readonly, which `go mod why` rejects: every Indirect-tab
// popup showed "Unable to determine" instead of the import chain.
func TestFindWhyPackageNeededReportsChain(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}

	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"app/go.mod":  "module example.com/app\n\ngo 1.21\n\nrequire example.com/dep v1.0.0\n\nreplace example.com/dep => ../dep\n",
		"app/main.go": "package main\n\nimport _ \"example.com/dep\"\n\nfunc main() {}\n",
		"dep/go.mod":  "module example.com/dep\n\ngo 1.21\n",
		"dep/dep.go":  "package dep\n",
	})

	got := strings.Join(FindWhyPackageNeeded(filepath.Join(root, "app"), "example.com/dep"), "\n")

	if strings.Contains(got, "Unable to determine") {
		t.Fatalf("go mod why failed: %s", got)
	}
	if !strings.Contains(got, "example.com/app") || !strings.Contains(got, "example.com/dep") {
		t.Errorf("chain should run from the main module to the dependency, got:\n%s", got)
	}
}

// A -mod in the user's GOFLAGS must not break the popup: go mod why does not
// define the flag, so it is skipped rather than rejected.
func TestFindWhyPackageNeededToleratesGOFLAGS(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	t.Setenv("GOFLAGS", "-mod=mod")

	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"app/go.mod":  "module example.com/app\n\ngo 1.21\n\nrequire example.com/dep v1.0.0\n\nreplace example.com/dep => ../dep\n",
		"app/main.go": "package main\n\nimport _ \"example.com/dep\"\n\nfunc main() {}\n",
		"dep/go.mod":  "module example.com/dep\n\ngo 1.21\n",
		"dep/dep.go":  "package dep\n",
	})

	got := strings.Join(FindWhyPackageNeeded(filepath.Join(root, "app"), "example.com/dep"), "\n")
	if strings.Contains(got, "Unable to determine") {
		t.Fatalf("go mod why failed under GOFLAGS=-mod=mod: %s", got)
	}
}
