package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindModuleRootWalksUp(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	deep := filepath.Join(root, "pkg", "inner", "deeper")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	// Running inside a subpackage must find the module root, the way the go
	// tool does; the previous version only ever looked in the cwd.
	got := findModuleRoot(deep)
	want := filepath.Join(root, "go.mod")
	if got != want {
		t.Errorf("findModuleRoot(%q) = %q, want %q", deep, got, want)
	}
}

func TestFindModuleRootFallsBackToStartDir(t *testing.T) {
	// A temp dir with no go.mod anywhere up the chain (until, possibly, the
	// filesystem root — which will not have one).
	dir := t.TempDir()

	got := findModuleRoot(dir)
	if got != filepath.Join(dir, "go.mod") {
		t.Errorf("findModuleRoot(%q) = %q, want the start dir so the error names it", dir, got)
	}
}

func TestFindModuleRootIgnoresGoModDirectory(t *testing.T) {
	root := t.TempDir()
	// A directory literally named go.mod must not be mistaken for the file.
	if err := os.Mkdir(filepath.Join(root, "go.mod"), 0o755); err != nil {
		t.Fatal(err)
	}

	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := findModuleRoot(sub); got == filepath.Join(root, "go.mod") {
		t.Error("a go.mod directory was treated as the module file")
	}
}

func TestResolveGoModPathModes(t *testing.T) {
	dir := t.TempDir()

	t.Run("gomod flag wins", func(t *testing.T) {
		target := filepath.Join(dir, "go.mod")
		got, err := resolveGoModPath(target, dir)
		if err != nil {
			t.Fatal(err)
		}
		if got != target {
			t.Errorf("got %q, want %q", got, target)
		}
	})

	// A non-go.mod filename would be edited in place while `go get` and
	// `go mod tidy` operated on the directory's real go.mod — two different
	// files, with rollback restoring a go.sum from the wrong module.
	t.Run("gomod flag rejects alternate filename", func(t *testing.T) {
		_, err := resolveGoModPath(filepath.Join(dir, "custom.mod"), "")
		if err == nil {
			t.Fatal("expected --gomod custom.mod to be rejected")
		}
		if !strings.Contains(err.Error(), "go.mod") {
			t.Errorf("error should name the expected filename, got %q", err)
		}
	})

	t.Run("gomod flag accepts a relative go.mod", func(t *testing.T) {
		got, err := resolveGoModPath("./go.mod", "")
		if err != nil {
			t.Fatal(err)
		}
		if !filepath.IsAbs(got) {
			t.Errorf("got %q, want an absolute path", got)
		}
	})

	t.Run("dir flag appends go.mod", func(t *testing.T) {
		got, err := resolveGoModPath("", dir)
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(dir, "go.mod"); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("relative paths are absolute", func(t *testing.T) {
		got, err := resolveGoModPath("", ".")
		if err != nil {
			t.Fatal(err)
		}
		if !filepath.IsAbs(got) {
			t.Errorf("got %q, want an absolute path", got)
		}
	})
}

func TestMaxUpdateIsValidatedBeforeAnythingRuns(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // run() opens the log file under $HOME
	t.Cleanup(func() { rootCmd.SetArgs(nil); maxUpdate = "major" })

	rootCmd.SetArgs([]string{"1.25", "--dir", t.TempDir(), "--max-update", "huge"})
	err := rootCmd.Execute()

	if err == nil || !strings.Contains(err.Error(), "--max-update") {
		t.Errorf("an unknown level should be rejected by name, got %v", err)
	}
}
