//go:build !windows

package e2e

import (
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

func (tm *term) expectMatch(re string) {
	tm.t.Helper()
	rx := regexp.MustCompile(re)
	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		if loc := rx.FindStringIndex(tm.text()); loc != nil {
			tm.mu.Lock()
			tm.mark = tm.buf.Len()
			tm.mu.Unlock()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	tm.t.Fatalf("timed out waiting for /%s/; output since last match:\n%s", re, tm.text())
}

// startAnalysis presses Enter on the welcome screen and waits for the result.
func startAnalysis(tm *term) {
	tm.expect("Press Enter")
	tm.send("\r")
	tm.expect("Select Modules to Update")
}

func TestDryRunListsAvailableUpdates(t *testing.T) {
	p := newFakeProxy(t)
	twoReleases(p, "example.com/dep1", "example.com/dep2")
	sb := newSandbox(t, p, "example.com/dep1", "example.com/dep2")

	out, err := sb.run("--dry-run")
	if err != nil {
		t.Fatalf("dry run failed: %v\n%s", err, out)
	}
	for _, want := range []string{"example.com/dep1: v1.0.0 → v1.1.0", "example.com/dep2: v1.0.0 → v1.1.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(sb.read("go.mod"), "example.com/dep1 v1.0.0") {
		t.Error("a dry run changed go.mod")
	}
}

// go list leaves GoVersion out for a release whose go directive is newer than the
// installed toolchain. Reading that as "no requirement" proposed exactly the
// releases that must be refused.
func TestReleasesNeedingANewerGoAreNeverProposed(t *testing.T) {
	p := newFakeProxy(t)
	p.publish("example.com/dep1",
		release{"v0.9.0", "1.21"}, release{"v1.0.0", "1.21"},
		release{"v1.1.0", "1.99"}, release{"v1.2.0", "1.99"})
	sb := newSandbox(t, p, "example.com/dep1")
	sb.write("go.mod", strings.Replace(sb.read("go.mod"), "dep1 v1.0.0", "dep1 v0.9.0", 1))

	out, err := sb.run("--dry-run")
	if err != nil {
		t.Fatalf("dry run failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "example.com/dep1: v0.9.0 → v1.0.0") {
		t.Errorf("should stop at v1.0.0, the newest release a Go 1.21 module can use:\n%s", out)
	}
	if !strings.Contains(out, "2 newer version(s) need a later Go") {
		t.Errorf("should say two newer releases were refused:\n%s", out)
	}
}

func TestFilterSelectUpdateAndReturn(t *testing.T) {
	p := newFakeProxy(t)
	twoReleases(p, "example.com/dep1", "example.com/dep2", "example.com/dep3")
	sb := newSandbox(t, p, "example.com/dep1", "example.com/dep2", "example.com/dep3")
	tm := sb.tui()

	startAnalysis(tm)

	// "q" inside the filter is a letter, not quit; dep names have none, so type
	// one anyway to prove it.
	tm.send("/")
	tm.expect("Filter mode")
	tm.send("dep2")
	tm.expect("Filter: dep2")
	tm.send("\r")
	tm.expect("Filter: 'dep2'")
	tm.send(" ")
	tm.expect("selected: example.com/dep2")
	tm.send("\r")
	tm.expect("Update Complete!")

	gomod := sb.read("go.mod")
	if !strings.Contains(gomod, "example.com/dep2 v1.1.0") {
		t.Errorf("dep2 was not updated:\n%s", gomod)
	}
	if !strings.Contains(gomod, "example.com/dep1 v1.0.0") || !strings.Contains(gomod, "example.com/dep3 v1.0.0") {
		t.Errorf("modules outside the selection were touched:\n%s", gomod)
	}

	// Back to the list: the Require tab must show what is on disk now.
	tm.send("\r")
	tm.expect("Select Modules to Update")
	tm.send("\t")
	tm.expectMatch(`example\.com/dep2\s+v1\.1\.0`)

	tm.send("q")
	if code := tm.wait(quick); code != 0 {
		t.Errorf("exit code %d", code)
	}
}

// Quitting while analysis is running used to exit before the worker could delete
// the scratch module, so every such quit left a directory behind.
func TestQuitDuringAnalysisLeavesNothingBehind(t *testing.T) {
	p := newFakeProxy(t)
	twoReleases(p, "example.com/dep1", "example.com/dep2", "example.com/dep3")
	p.delay("*", 700*time.Millisecond)
	sb := newSandbox(t, p, "example.com/dep1", "example.com/dep2", "example.com/dep3")
	tm := sb.tui()

	tm.expect("Press Enter")
	tm.send("\r")
	tm.expect("Checking")
	if len(sb.scratchDirs()) == 0 {
		t.Fatal("precondition: analysis should have created its scratch module by now")
	}

	start := time.Now()
	tm.send("q")
	if code := tm.wait(quick); code != 0 {
		t.Errorf("exit code %d", code)
	}
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Errorf("quitting took %v", elapsed)
	}
	if left := sb.scratchDirs(); len(left) != 0 {
		t.Errorf("scratch directories left behind: %v", left)
	}
}

// updateInFlight walks to the point where go get is blocked downloading a zip.
func updateInFlight(t *testing.T) (*sandbox, *term, string) {
	p := newFakeProxy(t)
	twoReleases(p, "example.com/dep1", "example.com/dep2", "example.com/dep3")
	sb := newSandbox(t, p, "example.com/dep1", "example.com/dep2", "example.com/dep3")
	sb.target = "1.22"
	original := sb.read("go.mod")
	tm := sb.tui()

	startAnalysis(tm)
	p.delay("zip", 20*time.Second) // analysis only needs .info/.mod; go get needs the zip
	tm.send("a")
	tm.expect("Selected 3 module(s)")
	tm.send("\r")
	tm.expect("Updating Modules")
	time.Sleep(700 * time.Millisecond) // let go get start and block on the download
	return sb, tm, original
}

func assertRolledBack(t *testing.T, sb *sandbox, original string) {
	t.Helper()
	if got := sb.read("go.mod"); got != original {
		t.Errorf("go.mod was left modified:\n%s", got)
	}
	if sum := sb.read("go.sum"); sum != "" {
		t.Errorf("a go.sum that did not exist before was left behind:\n%s", sum)
	}
}

// Ctrl+C during an update used to exit at once, leaving go get running and
// go.mod half-updated.
func TestCtrlCDuringUpdateCancelsAndRollsBack(t *testing.T) {
	sb, tm, original := updateInFlight(t)

	tm.send("\x03")
	tm.expect("Update Cancelled")
	assertRolledBack(t, sb, original)

	tm.send("q")
	if code := tm.wait(quick); code != 0 {
		t.Errorf("exit code %d", code)
	}
}

// Closing the terminal mid-update is not trapped by Bubble Tea.
func TestHangupDuringUpdateRollsBack(t *testing.T) {
	sb, tm, original := updateInFlight(t)

	tm.signal(syscall.SIGHUP)
	tm.wait(quick)
	assertRolledBack(t, sb, original)
}
