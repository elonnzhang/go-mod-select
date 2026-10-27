//go:build !windows

package e2e

import (
	"bytes"
	"os/exec"
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
		release{version: "v0.9.0", goDir: "1.21"}, release{version: "v1.0.0", goDir: "1.21"},
		release{version: "v1.1.0", goDir: "1.99"}, release{version: "v1.2.0", goDir: "1.99"})
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
	sb := newSandbox(t, p, "example.com/dep1", "example.com/dep2", "example.com/dep3")
	p.delay("*", 700*time.Millisecond)
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
func updateInFlight(t *testing.T) (*sandbox, *term, files) {
	p := newFakeProxy(t)
	twoReleases(p, "example.com/dep1", "example.com/dep2", "example.com/dep3")
	sb := newSandbox(t, p, "example.com/dep1", "example.com/dep2", "example.com/dep3")
	sb.target = "1.22"
	original := sb.snapshot()
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

func assertRolledBack(t *testing.T, sb *sandbox, want files) {
	t.Helper()
	got := sb.snapshot()
	if got.mod != want.mod {
		t.Errorf("go.mod was left modified:\n%s", got.mod)
	}
	if got.sum != want.sum {
		t.Errorf("go.sum was left modified:\n%s", got.sum)
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

// cli is a non-interactive run that a test can signal while it works.
type cli struct {
	cmd *exec.Cmd
	out *bytes.Buffer
}

func (sb *sandbox) startCLI(args ...string) *cli {
	sb.t.Helper()
	c := &cli{cmd: exec.Command(bin, append([]string{sb.target, "--dir", sb.dir}, args...)...), out: &bytes.Buffer{}}
	c.cmd.Env = sb.env
	c.cmd.Dir = sb.dir
	c.cmd.Stdout, c.cmd.Stderr = c.out, c.out
	if err := c.cmd.Start(); err != nil {
		sb.t.Fatal(err)
	}
	sb.t.Cleanup(func() { c.cmd.Process.Kill(); c.cmd.Wait() })
	return c
}

// finish waits for the run to end and returns its output and whether it
// exited non-zero.
func (c *cli) finish(t *testing.T) (string, bool) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case err := <-done:
		return c.out.String(), err != nil
	case <-time.After(waitTimeout):
		t.Fatalf("still running after %v; output:\n%s", waitTimeout, c.out.String())
		return "", false
	}
}

// Without catching the signal, Ctrl+C killed go get and this process together,
// and nothing rolled go.mod back.
func TestAutoModeInterruptedMidUpdateRollsBack(t *testing.T) {
	p := newFakeProxy(t)
	twoReleases(p, "example.com/dep1", "example.com/dep2")
	sb := newSandbox(t, p, "example.com/dep1", "example.com/dep2")
	sb.target = "1.22"
	original := sb.snapshot()
	p.delay("zip", 20*time.Second)
	p.resetHits()

	run := sb.startCLI("--auto")
	p.waitForHit(t, "zip") // go get is now blocked downloading
	run.cmd.Process.Signal(syscall.SIGINT)

	out, failed := run.finish(t)
	if !failed || !strings.Contains(out, "interrupted") {
		t.Errorf("an interrupted run should fail with \"interrupted\" (failed=%v):\n%s", failed, out)
	}
	assertRolledBack(t, sb, original)
}

// An interrupted analysis must clean up after itself like a finished one.
func TestDryRunInterruptedMidAnalysisLeavesNothingBehind(t *testing.T) {
	p := newFakeProxy(t)
	twoReleases(p, "example.com/dep1", "example.com/dep2")
	sb := newSandbox(t, p, "example.com/dep1", "example.com/dep2")
	p.delay("*", 20*time.Second)
	p.resetHits()

	run := sb.startCLI("--dry-run")
	p.waitForHit(t, "list")
	if len(sb.scratchDirs()) == 0 {
		t.Fatal("precondition: the analysis should have a scratch module by now")
	}
	run.cmd.Process.Signal(syscall.SIGINT)

	out, failed := run.finish(t)
	if !failed || !strings.Contains(out, "interrupted") {
		t.Errorf("want a failed run saying \"interrupted\" (failed=%v):\n%s", failed, out)
	}
	if left := sb.scratchDirs(); len(left) != 0 {
		t.Errorf("scratch directories left behind: %v", left)
	}
}

// A lookup that fails is not "already up to date". The plan used to leave such
// modules out without a word.
func TestDryRunNamesModulesItCouldNotAnalyze(t *testing.T) {
	p := newFakeProxy(t)
	twoReleases(p, "example.com/dep1")
	sb := newSandbox(t, p, "example.com/dep1")
	sb.write("go.mod", sb.read("go.mod")+"\nrequire example.com/ghost v1.0.0\n")

	out, err := sb.run("--dry-run")
	if err != nil {
		t.Fatalf("dry run failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "example.com/dep1: v1.0.0 → v1.1.0") {
		t.Errorf("the module that could be analyzed is missing:\n%s", out)
	}
	if !strings.Contains(out, "1 module(s) could not be analyzed") || !strings.Contains(out, "example.com/ghost") {
		t.Errorf("the failed lookup should be named:\n%s", out)
	}
}

// breakingDep publishes a dependency whose newer release drops the function the
// application calls: the update applies cleanly and the project stops building.
func breakingDep(p *fakeProxy) {
	p.publish("example.com/dep1",
		release{version: "v1.0.0", goDir: "1.21", source: "package dep1\n\nfunc Hello() {}\n"},
		release{version: "v1.1.0", goDir: "1.21", source: "package dep1\n"})
}

const callsHello = "package main\n\nimport \"example.com/dep1\"\n\nfunc main() { dep1.Hello() }\n"

func TestVerifyRollsBackAnUpdateThatBreaksTheBuild(t *testing.T) {
	p := newFakeProxy(t)
	breakingDep(p)
	twoReleases(p, "example.com/dep2")
	sb := newSandbox(t, p, "example.com/dep1", "example.com/dep2")
	sb.write("main.go", callsHello)
	sb.target = "1.22"
	original := sb.snapshot()

	out, err := sb.run("--auto", "--verify")

	if err == nil || !strings.Contains(out, "no longer builds") {
		t.Errorf("want a failure saying the project no longer builds (err=%v):\n%s", err, out)
	}
	// dep2's harmless update is undone as well: the run is all or nothing.
	assertRolledBack(t, sb, original)
}

func TestVerifyKeepsAnUpdateThatStillBuilds(t *testing.T) {
	p := newFakeProxy(t)
	twoReleases(p, "example.com/dep1", "example.com/dep2")
	sb := newSandbox(t, p, "example.com/dep1", "example.com/dep2")

	out, err := sb.run("--auto", "--verify")

	if err != nil {
		t.Fatalf("a good update was rejected: %v\n%s", err, out)
	}
	if got := sb.read("go.mod"); !strings.Contains(got, "example.com/dep1 v1.1.0") || !strings.Contains(got, "example.com/dep2 v1.1.0") {
		t.Errorf("the update was not applied:\n%s", got)
	}
	if !strings.Contains(out, "go build ./... succeeded") {
		t.Errorf("the build check should have run:\n%s", out)
	}
}

// A project that did not build to begin with would fail every check, so the
// check must stand down rather than roll back every update.
func TestVerifyStandsDownWhenTheProjectAlreadyFailedToBuild(t *testing.T) {
	p := newFakeProxy(t)
	twoReleases(p, "example.com/dep1")
	sb := newSandbox(t, p, "example.com/dep1")
	// Still imports dep1, or go mod tidy would drop the requirement.
	sb.write("main.go", "package main\n\nimport _ \"example.com/dep1\"\n\nfunc main() { undefined() }\n")

	out, err := sb.run("--auto", "--verify")

	if err != nil {
		t.Fatalf("update should proceed when the baseline build already fails: %v\n%s", err, out)
	}
	if !strings.Contains(out, "does not build as it stands") {
		t.Errorf("the skipped check should be announced:\n%s", out)
	}
	if !strings.Contains(sb.read("go.mod"), "example.com/dep1 v1.1.0") {
		t.Error("the update was not applied")
	}
}

func TestVerifyToggleInTheTUIRollsBackABreakingUpdate(t *testing.T) {
	p := newFakeProxy(t)
	breakingDep(p)
	sb := newSandbox(t, p, "example.com/dep1")
	sb.write("main.go", callsHello)
	sb.target = "1.22"
	original := sb.snapshot()
	tm := sb.tui()

	startAnalysis(tm)
	tm.send("v")
	tm.expect("Build check on")
	tm.send("a")
	tm.expect("Selected 1 module(s)")
	tm.send("\r")
	tm.expect("Build Check Failed")

	assertRolledBack(t, sb, original)
	tm.send("q")
	if code := tm.wait(quick); code != 0 {
		t.Errorf("exit code %d", code)
	}
}

func TestPrereleasesAreSkippedUnlessAsked(t *testing.T) {
	p := newFakeProxy(t)
	p.publish("example.com/dep1",
		release{version: "v1.0.0", goDir: "1.21"}, release{version: "v1.1.0", goDir: "1.21"},
		release{version: "v1.2.0-rc1", goDir: "1.21"})
	sb := newSandbox(t, p, "example.com/dep1")

	out, err := sb.run("--dry-run")
	if err != nil {
		t.Fatalf("dry run failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "example.com/dep1: v1.0.0 → v1.1.0") || strings.Contains(out, "rc1") {
		t.Errorf("by default a release candidate must not be proposed:\n%s", out)
	}

	out, err = sb.run("--dry-run", "--pre")
	if err != nil {
		t.Fatalf("dry run --pre failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "example.com/dep1: v1.0.0 → v1.2.0-rc1 (prerelease)") {
		t.Errorf("--pre should offer the release candidate:\n%s", out)
	}
}

func TestMaxUpdateHoldsBackLargerJumps(t *testing.T) {
	p := newFakeProxy(t)
	p.publish("example.com/dep1",
		release{version: "v1.0.0", goDir: "1.21"}, release{version: "v1.0.5", goDir: "1.21"}, release{version: "v1.3.0", goDir: "1.21"})
	p.publish("example.com/dep2", release{version: "v1.0.0", goDir: "1.21"}, release{version: "v1.4.0", goDir: "1.21"})
	sb := newSandbox(t, p, "example.com/dep1", "example.com/dep2")

	out, err := sb.run("--dry-run", "--max-update", "patch")
	if err != nil {
		t.Fatalf("dry run failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "example.com/dep1: v1.0.0 → v1.0.5 (patch)") || !strings.Contains(out, "1 more held back by --max-update patch") {
		t.Errorf("dep1 should take the patch release and say a larger one exists:\n%s", out)
	}
	if !strings.Contains(out, "have newer versions beyond --max-update patch") || !strings.Contains(out, "example.com/dep2: v1.0.0 (1 newer)") {
		t.Errorf("dep2 has nothing within the limit and should be listed as held back:\n%s", out)
	}
	if strings.Contains(out, "dep2: v1.0.0 → ") {
		t.Errorf("dep2 must not be proposed an update:\n%s", out)
	}

	out, err = sb.run("--dry-run", "--max-update", "minor")
	if err != nil {
		t.Fatalf("dry run failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "example.com/dep1: v1.0.0 → v1.3.0") || !strings.Contains(out, "example.com/dep2: v1.0.0 → v1.4.0") {
		t.Errorf("within a major version both are in reach:\n%s", out)
	}

	if out, err := sb.run("--dry-run", "--max-update", "huge"); err == nil || !strings.Contains(out, "--max-update") {
		t.Errorf("a bad level should be rejected, got err=%v\n%s", err, out)
	}
}
