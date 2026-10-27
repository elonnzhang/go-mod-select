package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go-mod-select/pkg/finder"
	"go-mod-select/pkg/parser"
	"go-mod-select/pkg/updater"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TestSpinnerTickLapsesOutsideAnalyzing guards a idle-CPU leak: the tick chain
// started at launch and rescheduled itself forever, redrawing the whole screen
// ten times a second on screens that never show the spinner.
func TestSpinnerTickLapsesOutsideAnalyzing(t *testing.T) {
	m := benchModel(3)
	m.spinner = spinner.New()

	for _, screen := range []screen{screenWelcome, screenSelection, screenUpdating, screenComplete} {
		m.currentScreen = screen
		if _, cmd := m.Update(spinner.TickMsg{}); cmd != nil {
			t.Errorf("screen %d rescheduled the spinner tick", screen)
		}
	}

	m.currentScreen = screenAnalyzing
	if _, cmd := m.Update(spinner.TickMsg{}); cmd == nil {
		t.Error("the analyzing screen must keep its spinner ticking")
	}

	if cmd := m.Init(); cmd != nil {
		t.Error("Init should not start a spinner nobody can see yet")
	}
}

// TestCtrlCDuringUpdateCancelsInsteadOfQuitting guards the data-integrity bug
// where Ctrl+C mid-update exited at once: go get kept running unsupervised and
// go.mod was left half-updated with no rollback.
func TestCtrlCDuringUpdateCancelsInsteadOfQuitting(t *testing.T) {
	m := benchModel(2)
	m.currentScreen = screenUpdating
	cancelled := 0
	m.cancelUpdate = func() { cancelled++ }

	model, cmd := m.handleKeyPress(keyType(tea.KeyCtrlC))
	got := model.(AppModel)
	if cmd != nil {
		t.Fatal("first Ctrl+C quit the program instead of cancelling the update")
	}
	if cancelled != 1 || !got.cancelling {
		t.Errorf("cancel calls %d, cancelling %v; want 1 / true", cancelled, got.cancelling)
	}
	if !strings.Contains(got.renderUpdating(), "Cancelling") {
		t.Error("the updating screen does not say it is cancelling")
	}

	// The rollback is quick; if it ever hangs, a second Ctrl+C must still get out.
	if _, cmd := got.handleKeyPress(keyType(tea.KeyCtrlC)); cmd == nil {
		t.Error("second Ctrl+C should quit")
	}

	// Anywhere else Ctrl+C is an immediate quit.
	m.currentScreen = screenSelection
	if _, cmd := m.handleKeyPress(keyType(tea.KeyCtrlC)); cmd == nil {
		t.Error("Ctrl+C on the selection screen should quit")
	}
}

func TestCancelledRunSummary(t *testing.T) {
	m := benchModel(1)
	m.currentScreen = screenComplete
	m.updateFailed = true

	m.updateError = fmt.Errorf("failed to update x: %w", updater.ErrCancelled)
	view := m.renderComplete()
	for _, want := range []string{"Update Cancelled", "cancelled by user", "were restored"} {
		if !strings.Contains(view, want) {
			t.Errorf("cancelled summary lacks %q:\n%s", want, view)
		}
	}

	// A rollback that itself failed must never be reported as restored.
	m.updateError = fmt.Errorf("%w (%w)", updater.ErrCancelled, updater.ErrRollbackFailed)
	view = m.renderComplete()
	if !strings.Contains(view, "ROLLBACK INCOMPLETE") || strings.Contains(view, "were restored") {
		t.Errorf("failed rollback was presented as success:\n%s", view)
	}
}

func TestFailedRunDoesNotClaimTheRequestedGoVersion(t *testing.T) {
	m := benchModel(1)
	m.currentScreen = screenComplete
	m.updateFailed = true
	m.updateError = fmt.Errorf("boom")

	if view := m.renderComplete(); !strings.Contains(view, "unchanged (requested 1.25)") {
		t.Errorf("a rolled-back run should not list the target as the Go version:\n%s", view)
	}
}

func TestCompleteSummaryCapsLongLists(t *testing.T) {
	m := sized(benchModel(200), 120, 30)
	for _, mod := range m.updateModules {
		mod.Updated = true
	}
	m.currentScreen = screenComplete

	view := m.renderComplete()
	if got := strings.Count(view, "some-module-"); got >= 200 {
		t.Errorf("summary listed all %d modules; it would outgrow the terminal", got)
	}
	if !strings.Contains(view, "… and ") {
		t.Errorf("a truncated list should say how much was left out:\n%s", view)
	}
}

// TestReadOnlyTabsRefreshAfterUpdate guards stale data: the Require and
// Indirect tabs kept showing go.mod as it was at startup after an update.
func TestReadOnlyTabsRefreshAfterUpdate(t *testing.T) {
	m := sized(benchModel(1), 120, 40)
	m.currentScreen = screenUpdating
	m.goMod = &parser.GoMod{FilePath: "/x/go.mod"}
	m.requireModules = []parser.Require{{Path: "example.com/a", Version: "v1.0.0"}}

	fresh := &parser.GoMod{
		FilePath: "/x/go.mod",
		Requires: []parser.Require{
			{Path: "example.com/a", Version: "v2.0.0"},
			{Path: "example.com/b", Version: "v0.1.0", Indirect: true},
		},
		Replace: []parser.Replace{{OldPath: "example.com/a", NewPath: "../a"}},
	}
	model, _ := m.Update(updateCompleteMsg{refreshed: fresh})
	got := model.(AppModel)

	if got.requireModules[0].Version != "v2.0.0" {
		t.Errorf("Require tab still shows %s", got.requireModules[0].Version)
	}
	if len(got.indirectModules) != 1 || len(got.replaceModules) != 1 {
		t.Errorf("indirect %d, replace %d; want 1 / 1", len(got.indirectModules), len(got.replaceModules))
	}
	if got.currentScreen != screenComplete {
		t.Errorf("screen = %d, want the completion screen", got.currentScreen)
	}
}

func TestDetailPopupWrapsLongLines(t *testing.T) {
	m := sized(benchModel(1), 100, 40)
	long := "internal/" + strings.Repeat("segment/", 25) + "the_end.go"
	m.currentTab = tabRequire
	m.requireModules = []parser.Require{{Path: "example.com/a", Version: "v1.0.0"}}

	m = press(m, keyRunes(" "))
	if !m.showingDetail {
		t.Fatal("Space on a Require row should open the popup")
	}
	model, _ := m.Update(detailLoadedMsg{module: "example.com/a", body: "\n  • " + long + "\n", seq: m.detailSeq})
	m = model.(AppModel)

	// The viewport clips rather than wraps: unwrapped, the tail of the path
	// ("the_end.go") could never be scrolled into view.
	m.detailViewport.GotoBottom()
	if view := m.detailViewport.View(); !strings.Contains(view, "the_end.go") {
		t.Errorf("the end of a long line is unreachable in the popup:\n%s", view)
	}

	// Space, like Esc and q, closes it again.
	if m = press(m, keyRunes(" ")); m.showingDetail {
		t.Error("Space should close the popup")
	}
}

func TestWaitWorkersWaitsButNotForever(t *testing.T) {
	var wg sync.WaitGroup
	m := AppModel{workers: &wg}

	finished := false
	m.spawn(func() {
		time.Sleep(30 * time.Millisecond)
		finished = true
	})
	waitWorkers(&wg, 2*time.Second)
	if !finished {
		t.Error("waitWorkers returned before the worker's cleanup ran")
	}

	release := make(chan struct{})
	m.spawn(func() { <-release })
	start := time.Now()
	waitWorkers(&wg, 80*time.Millisecond)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("waitWorkers blocked %v on a stuck worker", elapsed)
	}
	close(release)
	waitWorkers(&wg, 2*time.Second)
}

// A rollback undoes only the run that failed. Modules applied by an earlier,
// successful run are still in go.mod, so they must stay marked as updated.
func TestFailedRoundKeepsEarlierRoundsApplied(t *testing.T) {
	m := sized(benchModel(3), 120, 40)
	m.updateModules[0].Updated = true // applied in round 1
	m.updateModules[1].Selected = true

	m = press(m, keyType(tea.KeyEnter)) // round 2 starts
	if m.currentScreen != screenUpdating {
		t.Fatalf("Enter did not start the update (screen %d)", m.currentScreen)
	}

	model, _ := m.Update(updateProgressMsg{current: 1, total: 1, module: m.updateModules[1].Info.Path, status: "success"})
	m = model.(AppModel)
	m.updateFailed = true
	model, _ = m.Update(updateCompleteMsg{})
	m = model.(AppModel)

	if !m.updateModules[0].Updated {
		t.Error("round 1's module was un-applied by round 2's rollback")
	}
	if m.updateModules[1].Updated {
		t.Error("the rolled-back module still shows as updated")
	}
}

func TestSummaryListsOnlyTheLatestRun(t *testing.T) {
	m := sized(benchModel(2), 120, 40)
	m.updateModules[0].Updated = true
	m.updatedBefore = map[string]bool{m.updateModules[0].Info.Path: true}
	m.updateModules[1].Updated = true
	m.currentScreen = screenComplete

	view := m.renderComplete()
	if !strings.Contains(view, "Modules updated:  1") || strings.Contains(view, m.updateModules[0].Info.Path) {
		t.Errorf("summary should cover only this run:\n%s", view)
	}
}

// Reopening a popup before the first load returns used to append its body twice.
func TestReopenedPopupIgnoresTheEarlierLoad(t *testing.T) {
	m := sized(benchModel(1), 100, 40)
	m.currentTab = tabRequire
	m.requireModules = []parser.Require{{Path: "example.com/a", Version: "v1.0.0"}}

	m = press(m, keyRunes(" "))
	firstSeq := m.detailSeq
	m = press(m, keyType(tea.KeyEsc))
	m = press(m, keyRunes(" "))

	for _, seq := range []int{firstSeq, m.detailSeq} {
		model, _ := m.Update(detailLoadedMsg{module: "example.com/a", body: "BODY\n", seq: seq})
		m = model.(AppModel)
	}
	if got := strings.Count(m.detailContent, "BODY"); got != 1 {
		t.Errorf("popup body appears %d times, want 1", got)
	}
}

func TestLongGoErrorsStayInsideTheScreen(t *testing.T) {
	stderr := "go get: exit status 1"
	for i := 0; i < 60; i++ {
		stderr += fmt.Sprintf("\nverifying example.com/mod%d@v1.0.0: checksum mismatch %s", i, strings.Repeat("x", 120))
	}
	err := fmt.Errorf("%s", stderr)

	m := sized(benchModel(1), 100, 30)
	m.currentScreen = screenUpdating
	model, _ := m.Update(updateFailedMsg{err: err})
	m = model.(AppModel)
	if len(m.updateLogs) > 10 {
		t.Errorf("the update log took %d rows for one error", len(m.updateLogs))
	}
	if h := lipgloss.Height(m.renderUpdating()); h > m.height {
		t.Errorf("updating screen is %d rows on a %d-row terminal", h, m.height)
	}

	m.currentScreen = screenComplete
	m.updateFailed, m.updateError = true, err
	if h := lipgloss.Height(m.renderComplete()); h > m.height {
		t.Errorf("summary is %d rows on a %d-row terminal", h, m.height)
	}
}

// A panic between two go commands must roll go.mod back, not report a rollback
// that never happened.
func TestUpdateWorkerPanicRollsBack(t *testing.T) {
	dir := t.TempDir()
	modPath := filepath.Join(dir, "go.mod")
	const original = "module example.com/x\n\ngo 1.21\n"
	if err := os.WriteFile(modPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	gm, err := parser.ParseGoMod(modPath)
	if err != nil {
		t.Fatal(err)
	}

	sub := make(chan tea.Msg, 8)
	ctx, cancel := context.WithCancel(context.Background())
	// A nil module makes the loop panic after the go directive was rewritten.
	startUpdateWorker(ctx, cancel, startUpdateMsg{
		modules:         []*finder.ModuleInfo{nil},
		targetGoVersion: "1.25",
		goMod:           gm,
	}, sub, make(chan struct{}))

	failed, ok := (<-sub).(updateFailedMsg)
	if !ok {
		t.Fatal("a panicking worker must report updateFailedMsg first")
	}
	if !rolledBack(failed.err) {
		t.Errorf("rollback reported as failed: %v", failed.err)
	}
	if _, ok := (<-sub).(updateCompleteMsg); !ok {
		t.Error("updateCompleteMsg must follow so the UI leaves the updating screen")
	}

	got, _ := os.ReadFile(modPath)
	if string(got) != original {
		t.Errorf("go.mod left half-updated after a panic:\n%s", got)
	}
}

func TestVerifyKeyTogglesTheBuildCheckOnTheUpdateTab(t *testing.T) {
	m := sized(semverModel(3), 120, 40)

	m = press(m, keyRunes("v"))
	if !m.verify {
		t.Fatal("v should turn the build check on")
	}
	if hint := m.selectionHint(); !strings.Contains(hint, "build check on") {
		t.Errorf("the hint should show the state: %q", hint)
	}
	m = press(m, keyRunes("v"))
	if m.verify {
		t.Error("v again should turn it off")
	}

	// It belongs to updating, so it does nothing where Enter cannot update.
	m = press(m, keyType(tea.KeyTab))
	if m = press(m, keyRunes("v")); m.verify {
		t.Error("v toggled the build check on a read-only tab")
	}

	// And while typing a filter it is just a letter.
	m = press(m, keyType(tea.KeyShiftTab))
	m = press(m, keyRunes("/"))
	if m = typeText(m, "v"); m.verify || m.filterInput != "v" {
		t.Errorf("v in the filter: verify=%v filter=%q", m.verify, m.filterInput)
	}
}

func TestBuildCheckProgressIsShownAndAFailureFailsTheRun(t *testing.T) {
	m := sized(semverModel(1), 120, 40)
	m.currentScreen = screenUpdating
	m.totalModules, m.currentModule = 1, 1

	step := func(status string) {
		model, _ := m.Update(updateProgressMsg{module: updater.VerifyLabel, status: status})
		m = model.(AppModel)
	}

	step("verifying")
	if !m.verifyRunning || !strings.Contains(m.renderUpdating(), "verifying the project still builds") {
		t.Error("the updating screen should say the build check is running")
	}
	if m.currentModule != 1 || m.totalModules != 1 {
		t.Errorf("the check is not a per-module step; counters became %d/%d", m.currentModule, m.totalModules)
	}

	step("error")
	if m.verifyRunning || !m.updateFailed {
		t.Errorf("a failed check should end the step and fail the run (running=%v failed=%v)", m.verifyRunning, m.updateFailed)
	}
	if last := m.updateLogs[len(m.updateLogs)-1]; !strings.Contains(last, "no longer builds") {
		t.Errorf("log should say why: %q", last)
	}

	m.currentScreen = screenComplete
	m.updateError = fmt.Errorf("%w: go build: exit status 1", updater.ErrVerifyFailed)
	if view := m.renderComplete(); !strings.Contains(view, "Build Check Failed") {
		t.Errorf("the summary should name the build check:\n%s", view)
	}
}

func TestSafeKeySelectsOnlyPatchAndMinorUpdates(t *testing.T) {
	m := sized(semverModel(5), 120, 40)
	types := []finder.UpdateType{finder.Patch, finder.Minor, finder.Major, finder.Prerelease, finder.Minor}
	for i, mod := range m.updateModules {
		mod.Info.UpdateType = types[i]
	}
	m.updateModules[4].Info.Downgrade = true // a minor-distance move backwards: not "safe"

	m = press(m, keyRunes("s"))

	var selected []int
	for i, mod := range m.updateModules {
		if mod.Selected {
			selected = append(selected, i)
		}
	}
	if len(selected) != 2 || selected[0] != 0 || selected[1] != 1 {
		t.Errorf("selected %v, want only the patch and the minor update [0 1]", selected)
	}

	// Pressing it again clears just those, and leaves what the user picked by hand.
	m.updateModules[2].Selected = true
	m = press(m, keyRunes("s"))
	if m.updateModules[0].Selected || m.updateModules[1].Selected || !m.updateModules[2].Selected {
		t.Error("the second press should clear the safe ones and leave the hand-picked major alone")
	}
}

func TestSafeKeyRespectsTheFilter(t *testing.T) {
	m := sized(semverModel(4), 120, 40)
	for _, mod := range m.updateModules {
		mod.Info.UpdateType = finder.Patch
	}
	m = press(m, keyRunes("/"))
	m = typeText(m, "some-module-2")
	m = press(m, keyType(tea.KeyEnter))

	m = press(m, keyRunes("s"))

	if m.countSelected() != 1 || !m.updateModules[2].Selected {
		t.Errorf("only the filtered module should be selected, got %d selected", m.countSelected())
	}
}

func TestWelcomeScreenStatesTheUpdatePolicy(t *testing.T) {
	m := semverModel(1)
	m.goMod = &parser.GoMod{Module: "example.com/x", GoVersion: "1.21"}

	info := m.renderGoModInfo()
	if !strings.Contains(info, "excluded") || !strings.Contains(info, "none") {
		t.Errorf("default policy should read: prereleases excluded, no limit:\n%s", info)
	}

	m.policy = finder.Policy{AllowPrerelease: true, MaxUpdate: finder.LimitMinor}
	info = m.renderGoModInfo()
	if !strings.Contains(info, "included") || !strings.Contains(info, "minor") {
		t.Errorf("policy not shown:\n%s", info)
	}
}

func TestRowsSayWhatMaxUpdateHeldBack(t *testing.T) {
	m := sized(semverModel(1), 140, 40)
	m.updateModules[0].Info.HeldBack = 3
	m = sized(m, 140, 40)

	if row := m.renderUpdateLine(m.updateModules[0], false); !strings.Contains(row, "3 held back by --max-update") {
		t.Errorf("row should explain the missing newer versions: %q", row)
	}
}
