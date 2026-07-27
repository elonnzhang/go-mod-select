package updater

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-mod-select/pkg/finder"
	"go-mod-select/pkg/parser"
)

const sampleGoMod = `module example.com/rollback

go 1.21

require github.com/fatih/color v1.14.1
`

const sampleGoSum = "github.com/fatih/color v1.14.1 h1:fake=\n"

func writeModule(t *testing.T, goSum string, withSum bool) (dir string, gm *parser.GoMod) {
	t.Helper()

	dir = t.TempDir()
	modPath := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(modPath, []byte(sampleGoMod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if withSum {
		if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte(goSum), 0o644); err != nil {
			t.Fatalf("write go.sum: %v", err)
		}
	}

	gm, err := parser.ParseGoMod(modPath)
	if err != nil {
		t.Fatalf("ParseGoMod: %v", err)
	}
	return dir, gm
}

func TestRollbackRestoresGoSum(t *testing.T) {
	dir, gm := writeModule(t, sampleGoSum, true)
	u := NewUpdater(gm)

	// Simulate what `go get` does to both files.
	if err := os.WriteFile(gm.FilePath, []byte("module mangled\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sumPath := filepath.Join(dir, "go.sum")
	if err := os.WriteFile(sumPath, []byte("github.com/other v1.0.0 h1:x=\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := u.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	gotMod, _ := os.ReadFile(gm.FilePath)
	if string(gotMod) != sampleGoMod {
		t.Errorf("go.mod not restored:\n%s", gotMod)
	}

	// The pre-fix bug: go.mod came back but go.sum kept the mutated content,
	// leaving a module that would not build.
	gotSum, err := os.ReadFile(sumPath)
	if err != nil {
		t.Fatalf("read go.sum: %v", err)
	}
	if string(gotSum) != sampleGoSum {
		t.Errorf("go.sum not restored, got %q want %q", gotSum, sampleGoSum)
	}
}

func TestRollbackRemovesGeneratedGoSum(t *testing.T) {
	dir, gm := writeModule(t, "", false)
	u := NewUpdater(gm)

	sumPath := filepath.Join(dir, "go.sum")
	if err := os.WriteFile(sumPath, []byte("github.com/x v1.0.0 h1:y=\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := u.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	if _, err := os.Stat(sumPath); !os.IsNotExist(err) {
		t.Errorf("go.sum should have been removed, stat err = %v", err)
	}
}

func TestRollbackWithNoGoSumIsNotAnError(t *testing.T) {
	_, gm := writeModule(t, "", false)
	u := NewUpdater(gm)

	if err := u.Rollback(); err != nil {
		t.Errorf("Rollback with no go.sum: %v", err)
	}
}

func TestUpdateAllRollsBackOnFailure(t *testing.T) {
	// GOPROXY=off makes the failing `go get` fail immediately instead of
	// spending ~45s on proxy retries for a module that does not exist.
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOFLAGS", "")

	dir, gm := writeModule(t, sampleGoSum, true)
	u := NewUpdater(gm)

	bad := []*finder.ModuleInfo{{
		Path:             "example.com/definitely/not/a/real/module",
		CurrentVersion:   "v1.0.0",
		LatestCompatible: "v9.9.9",
	}}

	if err := u.UpdateAll("1.22", bad, false); err == nil {
		t.Fatal("expected UpdateAll to fail")
	}

	gotMod, _ := os.ReadFile(gm.FilePath)
	if string(gotMod) != sampleGoMod {
		t.Errorf("go.mod not rolled back:\n%s", gotMod)
	}
	gotSum, _ := os.ReadFile(filepath.Join(dir, "go.sum"))
	if string(gotSum) != sampleGoSum {
		t.Errorf("go.sum not rolled back, got %q", gotSum)
	}
}

func TestVerifyGoVersionDetectsMismatch(t *testing.T) {
	_, gm := writeModule(t, sampleGoSum, true)
	u := NewUpdater(gm)

	// Stand in for what `go mod tidy` does when a dependency needs a newer
	// toolchain than the user asked for.
	raised := "module example.com/rollback\n\ngo 1.25.0\n"
	if err := os.WriteFile(gm.FilePath, []byte(raised), 0o644); err != nil {
		t.Fatal(err)
	}

	actual, matched, err := u.VerifyGoVersion("1.17")
	if err != nil {
		t.Fatalf("VerifyGoVersion: %v", err)
	}
	if actual != "1.25.0" {
		t.Errorf("actual = %q, want 1.25.0", actual)
	}
	if matched {
		t.Error("1.25.0 should not be reported as matching a requested 1.17")
	}
}

func TestVerifyGoVersionAcceptsNormalizedPatch(t *testing.T) {
	_, gm := writeModule(t, sampleGoSum, true)
	u := NewUpdater(gm)

	// The go tool writes "1.24.0" when asked for "1.24"; that is a match.
	if err := os.WriteFile(gm.FilePath, []byte("module example.com/rollback\n\ngo 1.24.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, matched, err := u.VerifyGoVersion("1.24")
	if err != nil {
		t.Fatalf("VerifyGoVersion: %v", err)
	}
	if !matched {
		t.Error("go 1.24.0 should satisfy a requested 1.24")
	}
}

// TestSecondRoundBackupIsFreshFromDisk guards the worst bug in this file's
// history: NewUpdater snapshotted parser.GoMod.Content, which is captured once
// at startup and never refreshed. A rollback in round 2 therefore restored the
// program-start go.mod and silently threw away round 1's applied upgrades.
func TestSecondRoundBackupIsFreshFromDisk(t *testing.T) {
	_, gm := writeModule(t, sampleGoSum, true)

	// Round 1 landed: the file on disk now differs from gm.Content.
	round1 := "module example.com/rollback\n\ngo 1.25.0\n\nrequire github.com/fatih/color v1.19.0\n"
	if err := os.WriteFile(gm.FilePath, []byte(round1), 0o644); err != nil {
		t.Fatal(err)
	}

	// Round 2 starts here and immediately rolls back.
	u := NewUpdater(gm)
	if err := os.WriteFile(gm.FilePath, []byte("module mangled\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := u.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	got, _ := os.ReadFile(gm.FilePath)
	if string(got) == sampleGoMod {
		t.Error("rollback reverted to the program-start go.mod, discarding round 1")
	}
	if string(got) != round1 {
		t.Errorf("rollback should restore round 1's state, got:\n%s", got)
	}
}

// TestGoDirectiveIsWrittenAfterAFailedRound covers the companion bug: a
// rolled-back UpdateGoVersion left gm.GoVersion set in memory, so the next
// round compared against it and skipped writing the directive entirely.
func TestGoDirectiveIsWrittenAfterAFailedRound(t *testing.T) {
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOFLAGS", "")

	_, gm := writeModule(t, sampleGoSum, true)

	// Round 1: the directive is written, then a module failure rolls it back.
	bad := []*finder.ModuleInfo{{
		Path:             "example.com/definitely/not/a/real/module",
		CurrentVersion:   "v1.0.0",
		LatestCompatible: "v9.9.9",
	}}
	if err := NewUpdater(gm).UpdateAll("1.25", bad, false); err == nil {
		t.Fatal("expected round 1 to fail")
	}

	// gm.GoVersion is now "1.25" in memory while the file says 1.21.
	if gm.GoVersion != "1.25" {
		t.Fatalf("precondition: gm.GoVersion = %q, want the stale 1.25", gm.GoVersion)
	}

	// Round 2 with no modules must still apply the directive.
	if err := NewUpdater(gm).applyGoVersion("1.25"); err != nil {
		t.Fatalf("applyGoVersion: %v", err)
	}

	got, _ := os.ReadFile(gm.FilePath)
	if !strings.Contains(string(got), "go 1.25") {
		t.Errorf("go directive was not written on the second attempt:\n%s", got)
	}
}

func TestApplyGoVersionSkipsWhenAlreadyCorrect(t *testing.T) {
	_, gm := writeModule(t, sampleGoSum, true)

	// The go tool normalizes 1.24 to 1.24.0; that must count as already applied
	// rather than triggering a pointless rewrite.
	if err := os.WriteFile(gm.FilePath, []byte("module example.com/rollback\n\ngo 1.24.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(gm.FilePath)

	if err := NewUpdater(gm).applyGoVersion("1.24"); err != nil {
		t.Fatalf("applyGoVersion: %v", err)
	}

	after, _ := os.ReadFile(gm.FilePath)
	if string(after) != string(before) {
		t.Errorf("file was rewritten despite already matching:\n%s", after)
	}
}

func TestRollbackPreservesFileMode(t *testing.T) {
	_, gm := writeModule(t, sampleGoSum, true)
	if err := os.Chmod(gm.FilePath, 0o600); err != nil {
		t.Skipf("chmod unsupported here: %v", err)
	}

	u := NewUpdater(gm)
	if err := os.WriteFile(gm.FilePath, []byte("module mangled\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := u.Rollback(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(gm.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	// A hard-coded 0o644 would widen a deliberately stricter mode.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
}
