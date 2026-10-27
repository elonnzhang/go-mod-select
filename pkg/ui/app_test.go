package ui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/elonnzhang/go-mod-select/pkg/config"
	"github.com/elonnzhang/go-mod-select/pkg/finder"
	"github.com/elonnzhang/go-mod-select/pkg/parser"

	tea "github.com/charmbracelet/bubbletea"
)

func testModel(paths ...string) AppModel {
	m := AppModel{
		currentScreen:   screenSelection,
		targetGoVersion: "1.25",
		colors:          config.DefaultColorConfig(),
		width:           120,
		height:          40,
		currentTab:      tabUpdate,
	}
	for _, p := range paths {
		m.updateModules = append(m.updateModules, &SelectableModule{
			Info: &finder.ModuleInfo{
				Path:             p,
				CurrentVersion:   "v1.0.0",
				LatestCompatible: "v1.1.0",
				UpdateType:       finder.Minor,
			},
		})
	}
	return m
}

func keyRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func keyType(t tea.KeyType) tea.KeyMsg {
	return tea.KeyMsg{Type: t}
}

// TestQuitKeyIsLiteralInFilterMode guards the bug where "q" was bound
// globally: typing a module path containing q quit the whole application.
func TestQuitKeyIsLiteralInFilterMode(t *testing.T) {
	m := testModel("github.com/quic-go/quic-go", "github.com/spf13/cobra")
	m.filterMode = true

	model, cmd := m.handleKeyPress(keyRunes("q"))
	got := model.(AppModel)

	if cmd != nil {
		t.Fatal("typing q in filter mode produced a command (likely tea.Quit)")
	}
	if got.filterInput != "q" {
		t.Errorf("filterInput = %q, want \"q\"", got.filterInput)
	}
	if !got.filterMode {
		t.Error("filter mode was exited")
	}
}

func TestQuitKeyStillQuitsOutsideFilter(t *testing.T) {
	m := testModel("github.com/spf13/cobra")

	_, cmd := m.handleKeyPress(keyRunes("q"))
	if cmd == nil {
		t.Error("q outside filter mode should quit")
	}
}

func TestCtrlCQuitsEvenInFilterMode(t *testing.T) {
	m := testModel("github.com/spf13/cobra")
	m.filterMode = true

	_, cmd := m.handleKeyPress(keyType(tea.KeyCtrlC))
	if cmd == nil {
		t.Error("ctrl+c should quit from filter mode")
	}
}

func TestFilterBackspaceTrimsWholeRune(t *testing.T) {
	m := testModel("example.com/mod")
	m.filterMode = true
	m.filterInput = "中文"

	model, _ := m.handleKeyPress(keyType(tea.KeyBackspace))
	if got := model.(AppModel).filterInput; got != "中" {
		t.Errorf("filterInput = %q, want %q", got, "中")
	}
}

// TestCompleteSummaryListsUpdatedModules guards the bug where the completion
// screen showed "Modules updated: 0": the Updated flag was set only when the
// user pressed Enter *after* the summary had already rendered.
func TestCompleteSummaryListsUpdatedModules(t *testing.T) {
	m := testModel("github.com/spf13/cobra", "golang.org/x/mod")
	m.updateModules[0].Selected = true
	m.currentScreen = screenUpdating
	m.updateSub = nil

	model, _ := m.Update(updateProgressMsg{
		current: 1, total: 1,
		module: "github.com/spf13/cobra",
		status: "success",
	})
	m = model.(AppModel)

	updated := m.updatedModules()
	if len(updated) != 1 {
		t.Fatalf("updatedModules() returned %d entries, want 1", len(updated))
	}
	if updated[0].Info.Path != "github.com/spf13/cobra" {
		t.Errorf("marked %q as updated", updated[0].Info.Path)
	}

	view := m.renderComplete()
	if !contains(view, "Modules updated:  1") {
		t.Errorf("summary does not report the update:\n%s", view)
	}
	if !contains(view, "github.com/spf13/cobra") {
		t.Errorf("summary omits the module name:\n%s", view)
	}
}

func TestFailedUpdateClearsUpdatedFlags(t *testing.T) {
	m := testModel("github.com/spf13/cobra")
	m.currentScreen = screenUpdating
	m.updateModules[0].Updated = true
	m.updateFailed = true

	model, _ := m.Update(updateCompleteMsg{})
	m = model.(AppModel)

	// A rollback undid the write, so nothing may be reported as applied.
	if len(m.updatedModules()) != 0 {
		t.Error("modules still marked updated after a rolled-back run")
	}
	if !contains(m.renderComplete(), "rolled back") {
		t.Error("failure summary does not mention the rollback")
	}
}

// TestAnalyzingLogScrollMovesWindow guards the bug where analyzingLogOffset was
// maintained by the key handler but ignored by the renderer, so the scroll keys
// did nothing.
func TestAnalyzingLogScrollMovesWindow(t *testing.T) {
	m := testModel()
	m.currentScreen = screenAnalyzing
	m.analyzingLogFollow = true
	for i := 0; i < 200; i++ {
		m.analysisLogs = append(m.analysisLogs, logPrefix(statusFound)+"module-"+string(rune('a'+i%26))+itoa(i))
	}
	m.analyzingLogOffset = m.maxAnalyzingLogOffset()

	tail := m.renderAnalyzingLogs()

	model, _ := m.handleKeyPress(keyType(tea.KeyPgUp))
	scrolled := model.(AppModel)

	if scrolled.analyzingLogOffset >= m.analyzingLogOffset {
		t.Fatalf("PgUp did not reduce offset: %d -> %d", m.analyzingLogOffset, scrolled.analyzingLogOffset)
	}
	if scrolled.analyzingLogFollow {
		t.Error("scrolling up should detach from the log tail")
	}
	if scrolled.renderAnalyzingLogs() == tail {
		t.Error("render output identical after scrolling; offset is being ignored")
	}
}

func TestAnalyzingLogFollowResumesAtBottom(t *testing.T) {
	m := testModel()
	m.currentScreen = screenAnalyzing
	for i := 0; i < 100; i++ {
		m.analysisLogs = append(m.analysisLogs, logPrefix(statusFound)+"m"+itoa(i))
	}
	m.analyzingLogOffset = 0
	m.analyzingLogFollow = false

	model, _ := m.handleKeyPress(keyRunes("G"))
	got := model.(AppModel)

	if got.analyzingLogOffset != got.maxAnalyzingLogOffset() {
		t.Errorf("G did not jump to the newest entry: offset %d, max %d",
			got.analyzingLogOffset, got.maxAnalyzingLogOffset())
	}
	if !got.analyzingLogFollow {
		t.Error("jumping to the bottom should re-enable follow")
	}
}

func TestUpdateOrAddAnalysisLogReplacesChecking(t *testing.T) {
	m := testModel()
	m.updateOrAddAnalysisLog(statusChecking, "example.com/a")
	m.updateOrAddAnalysisLog(statusChecking, "example.com/b")
	m.updateOrAddAnalysisLog(statusFound, "example.com/a")

	if len(m.analysisLogs) != 2 {
		t.Fatalf("got %d log lines, want 2 (the checking entry should be replaced): %v",
			len(m.analysisLogs), m.analysisLogs)
	}
	if m.analysisLogs[0] != logPrefix(statusFound)+"example.com/a" {
		t.Errorf("entry 0 = %q, want the FOUND replacement", m.analysisLogs[0])
	}
	if m.analysisLogs[1] != logPrefix(statusChecking)+"example.com/b" {
		t.Errorf("entry 1 = %q, want b still pending", m.analysisLogs[1])
	}
}

func TestToggleAllVisibleRespectsFilter(t *testing.T) {
	m := testModel("github.com/a/one", "github.com/b/two", "github.com/a/three")
	m.filterInput = "/a/"

	m.toggleAllVisible()

	if !m.updateModules[0].Selected || !m.updateModules[2].Selected {
		t.Error("filtered-in modules were not selected")
	}
	if m.updateModules[1].Selected {
		t.Error("filtered-out module was selected")
	}

	// Pressing again clears, so the key is its own undo.
	m.toggleAllVisible()
	if m.countSelected() != 0 {
		t.Errorf("second toggle left %d selected", m.countSelected())
	}
}

func TestToggleAllVisibleSkipsUpdated(t *testing.T) {
	m := testModel("github.com/a/one", "github.com/b/two")
	m.updateModules[0].Updated = true

	m.toggleAllVisible()

	if m.updateModules[0].Selected {
		t.Error("an already-applied module must not be selectable")
	}
	if !m.updateModules[1].Selected {
		t.Error("pending module should have been selected")
	}
}

func TestTabCyclesBothDirections(t *testing.T) {
	m := testModel("example.com/a")

	model, _ := m.handleKeyPress(keyType(tea.KeyTab))
	if got := model.(AppModel).currentTab; got != tabRequire {
		t.Errorf("Tab from Update went to %v, want Require", got)
	}

	m.currentTab = tabUpdate
	model, _ = m.handleKeyPress(keyType(tea.KeyShiftTab))
	if got := model.(AppModel).currentTab; got != tabReplace {
		t.Errorf("Shift+Tab from Update went to %v, want Replace (wrap)", got)
	}
}

func TestEmptyUpdateTabDistinguishesFilterFromNoUpdates(t *testing.T) {
	m := testModel()
	if got := m.renderModuleList(); !contains(got, "already at their newest") {
		t.Errorf("empty list should explain there is nothing to update, got %q", got)
	}

	m = testModel("github.com/spf13/cobra")
	m.filterInput = "zzzz"
	if got := m.renderModuleList(); !contains(got, "No modules match") {
		t.Errorf("filtered-empty list should blame the filter, got %q", got)
	}
}

func TestElidePathKeepsTail(t *testing.T) {
	long := "github.com/some/very/long/organization/name/deep/nested/package/path"
	got := elideTo(long, pathColumnWidth)

	if len([]rune(got)) > pathColumnWidth {
		t.Errorf("elided path is %d runes, want <= %d: %q", len([]rune(got)), pathColumnWidth, got)
	}
	// The tail is what distinguishes one dependency from another.
	if !contains(got, "package/path") {
		t.Errorf("elided path lost its tail: %q", got)
	}
	if short := elideTo("github.com/a/b", pathColumnWidth); short != "github.com/a/b" {
		t.Errorf("short path was modified: %q", short)
	}
}

func TestDowngradeRendersDistinctly(t *testing.T) {
	m := testModel("github.com/fatih/color")
	m.updateModules[0].Info.CurrentVersion = "v1.19.0"
	m.updateModules[0].Info.LatestCompatible = "v1.18.0"
	m.updateModules[0].Info.Downgrade = true

	line := m.renderUpdateLine(m.updateModules[0], false)
	if !contains(line, "↓") {
		t.Errorf("downgrade should not render as a plain update arrow: %q", line)
	}
}

func TestGetViewportHeightStaysPositive(t *testing.T) {
	m := testModel()
	m.height = 4 // shorter than the fixed chrome

	if got := m.getViewportHeight(); got < 1 {
		t.Errorf("getViewportHeight() = %d on a tiny terminal, want >= 1", got)
	}
}

func TestNoDirectDepsCompletesImmediately(t *testing.T) {
	m := testModel()
	m.goMod = &parser.GoMod{
		Module: "example.com/leaf",
		Requires: []parser.Require{
			{Path: "example.com/dep", Version: "v1.0.0", Indirect: true},
		},
	}

	msg := m.analyzeModules()()
	done, ok := msg.(analyzeCompleteMsg)
	if !ok {
		t.Fatalf("a module with no direct deps should complete without analysis, got %T", msg)
	}
	if len(done.indirectModules) != 1 {
		t.Errorf("indirect deps should still be reported, got %d", len(done.indirectModules))
	}
}

// helpers

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

func itoa(i int) string {
	return strconv.Itoa(i)
}
