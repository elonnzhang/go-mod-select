package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elonnzhang/go-mod-select/pkg/parser"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// sized returns m laid out for a w×h terminal, the way a WindowSizeMsg would.
func sized(m AppModel, w, h int) AppModel {
	model, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return model.(AppModel)
}

func press(m AppModel, msg tea.KeyMsg) AppModel {
	model, _ := m.handleKeyPress(msg)
	return model.(AppModel)
}

func typeText(m AppModel, s string) AppModel {
	for _, r := range s {
		m = press(m, keyRunes(string(r)))
	}
	return m
}

// semverModel is a list with ordinary-length versions, which is what most
// go.mod files contain.
func semverModel(n int) AppModel {
	m := benchModel(n)
	for _, mod := range m.updateModules {
		mod.Info.CurrentVersion = "v1.2.3"
		mod.Info.LatestCompatible = "v1.14.0"
	}
	return m
}

// TestSelectionScreenFitsTerminal guards two layout bugs: the one-line help
// hint wrapped on terminals under ~100 columns, adding a row that made the
// screen taller than the terminal (bubbletea then drops the top row); and rows
// wider than the terminal were cut off, hiding the new version.
func TestSelectionScreenFitsTerminal(t *testing.T) {
	for _, h := range []int{24, 30, 40, 55} {
		for _, w := range []int{70, 80, 100, 120, 160} {
			m := sized(semverModel(200), w, h)
			view := m.View()

			if got := lipgloss.Height(view); got != h-1 {
				t.Errorf("%dx%d: screen is %d rows, want %d", w, h, got, h-1)
			}
			for _, line := range strings.Split(view, "\n") {
				if lipgloss.Width(line) > w {
					t.Errorf("%dx%d: row is %d columns wide: %q", w, h, lipgloss.Width(line), line)
					break
				}
			}
			if !strings.Contains(view, "v1.14.0") {
				t.Errorf("%dx%d: the new version is not visible:\n%s", w, h, view)
			}
		}
	}
}

func TestHeaderHintNeverWraps(t *testing.T) {
	base := lipgloss.Height(sized(semverModel(5), 200, 40).renderSelectionHeader())

	for w := 50; w <= 200; w += 10 {
		for _, tab := range []tabType{tabUpdate, tabRequire, tabReplace} {
			for _, filter := range []string{"", "cobra", strings.Repeat("x", 45)} {
				for _, mode := range []bool{false, true} {
					m := sized(semverModel(5), w, 40)
					m.currentTab, m.filterInput, m.filterMode = tab, filter, mode

					if got := lipgloss.Height(m.renderSelectionHeader()); got != base {
						t.Errorf("width %d tab %d filter %q mode %v: header is %d rows, want %d",
							w, tab, filter, mode, got, base)
					}
				}
			}
		}
	}
}

func TestRenderModuleListOnlyRendersVisibleWindow(t *testing.T) {
	m := sized(benchModel(500), 120, 40)
	out := m.renderModuleList()

	if got, want := strings.Count(out, "some-module-"), m.getViewportHeight(); got != want {
		t.Errorf("rendered %d rows, want only the %d on screen", got, want)
	}
	if got, want := lipgloss.Height(out), m.getViewportHeight(); got != want {
		t.Errorf("list is %d rows tall, want %d so the footer stays pinned", got, want)
	}
}

func TestCursorMovementScrollsTheWindow(t *testing.T) {
	m := sized(semverModel(100), 120, 40)
	h := m.getViewportHeight()

	for i := 0; i < h+4; i++ {
		m = press(m, keyType(tea.KeyDown))
	}
	if m.cursor != h+4 || m.listTop != 5 {
		t.Fatalf("after %d downs: cursor %d top %d, want cursor %d top 5", h+4, m.cursor, m.listTop, h+4)
	}
	if out := m.renderModuleList(); !strings.Contains(out, fmt.Sprintf("some-module-%d", h+4)) {
		t.Errorf("cursor row is not on screen:\n%s", out)
	}

	m = press(m, keyType(tea.KeyEnd))
	if m.cursor != 99 || m.listTop != 100-h {
		t.Errorf("End: cursor %d top %d, want 99 / %d", m.cursor, m.listTop, 100-h)
	}

	m = press(m, keyType(tea.KeyPgUp))
	if want := 99 - (h - 1); m.cursor != want {
		t.Errorf("PgUp moved to %d, want a screenful (%d rows) up = %d", m.cursor, h-1, want)
	}

	m = press(m, keyType(tea.KeyHome))
	if m.cursor != 0 || m.listTop != 0 {
		t.Errorf("Home: cursor %d top %d, want 0 / 0", m.cursor, m.listTop)
	}
}

func TestResizeKeepsCursorOnScreen(t *testing.T) {
	m := sized(semverModel(100), 120, 60)
	m = press(m, keyType(tea.KeyEnd))

	m = sized(m, 120, 24)
	h := m.getViewportHeight()
	if m.cursor < m.listTop || m.cursor >= m.listTop+h {
		t.Errorf("after shrinking, cursor %d is outside the window [%d,%d)", m.cursor, m.listTop, m.listTop+h)
	}
}

func TestFilterAppliesToEveryTab(t *testing.T) {
	m := sized(semverModel(3), 120, 40)
	m.requireModules = []parser.Require{
		{Path: "github.com/spf13/cobra", Version: "v1.0.0"},
		{Path: "golang.org/x/mod", Version: "v0.1.0"},
	}
	m = press(m, keyType(tea.KeyTab))
	if m.currentTab != tabRequire {
		t.Fatalf("Tab went to %v", m.currentTab)
	}

	// The filter used to enter filter mode on read-only tabs and then change
	// nothing.
	m = press(m, keyRunes("/"))
	m = typeText(m, "x/mod")
	m = press(m, keyType(tea.KeyEnter))

	if got := m.visibleCount(); got != 1 {
		t.Fatalf("filter matched %d entries, want 1", got)
	}
	if out := m.renderModuleList(); strings.Contains(out, "cobra") || !strings.Contains(out, "x/mod") {
		t.Errorf("list does not reflect the filter:\n%s", out)
	}

	// The cursor addresses the filtered list, so details must come from the
	// matching entry, not the entry at the same position in the full list.
	header, ok := m.detailHeader()
	if !ok || !strings.Contains(header, "golang.org/x/mod") {
		t.Errorf("detail popup describes the wrong module: %q", header)
	}
}

func TestArrowsMoveWhileFiltering(t *testing.T) {
	m := sized(semverModel(10), 120, 40)
	m = press(m, keyRunes("/"))
	m = typeText(m, "some-module")

	m = press(m, keyType(tea.KeyDown))
	m = press(m, keyType(tea.KeyDown))
	if m.cursor != 2 || !m.filterMode {
		t.Fatalf("cursor %d filterMode %v, want 2 / true", m.cursor, m.filterMode)
	}

	// j/k are literal characters while typing, not movement.
	m = press(m, keyRunes("k"))
	if m.filterInput != "some-modulek" {
		t.Errorf("filterInput = %q", m.filterInput)
	}
	m = press(m, keyType(tea.KeyBackspace))

	// Editing the filter changes what the cursor points at, so it returns to
	// the top; Enter then applies the filter without moving it again.
	if m.cursor != 0 {
		t.Errorf("cursor %d after editing the filter, want 0", m.cursor)
	}
	m = press(m, keyType(tea.KeyDown))
	m = press(m, keyType(tea.KeyEnter))
	if m.filterMode || m.cursor != 1 {
		t.Errorf("Enter should apply the filter and keep the cursor: mode %v cursor %d", m.filterMode, m.cursor)
	}
}

func TestEnterOnReadOnlyTabExplainsItself(t *testing.T) {
	m := sized(semverModel(2), 120, 40)
	m = press(m, keyType(tea.KeyTab))
	m = press(m, keyType(tea.KeyEnter))

	if n := len(m.operationLogs); n == 0 || !strings.Contains(m.operationLogs[n-1], "Update tab") {
		t.Errorf("Enter on a read-only tab gave no hint: %v", m.operationLogs)
	}
}

func TestPathColumnShrinksToFitAndWidensForShortPaths(t *testing.T) {
	m := sized(semverModel(3), 120, 40)
	if m.pathCol >= pathColumnWidth {
		t.Errorf("short paths should not reserve %d columns, got %d", pathColumnWidth, m.pathCol)
	}

	m.updateModules[0].Info.Path = "github.com/" + strings.Repeat("deep/", 20) + "pkg"
	m = sized(m, 80, 40)
	row := m.renderUpdateLine(m.updateModules[0], false)
	if lipgloss.Width(row) > 80 {
		t.Errorf("row is %d columns on an 80-column terminal", lipgloss.Width(row))
	}
	if !strings.Contains(row, "v1.14.0") {
		t.Errorf("version column was squeezed out: %q", row)
	}
}

func TestElideToIsRuneAware(t *testing.T) {
	got := elideTo("example.com/日本語/パッケージ", 8)
	if len([]rune(got)) != 8 || !strings.HasSuffix(got, "パッケージ") {
		t.Errorf("elideTo cut through a multi-byte rune or lost the tail: %q", got)
	}
}

func TestEmptyTabsSurviveEveryNavigationKey(t *testing.T) {
	m := sized(semverModel(0), 120, 40)
	for _, tab := range []tabType{tabUpdate, tabRequire, tabIndirect, tabReplace} {
		m.currentTab = tab
		for _, k := range []tea.KeyType{tea.KeyPgDown, tea.KeyDown, tea.KeyEnd, tea.KeyUp, tea.KeyPgUp, tea.KeyHome} {
			m = press(m, keyType(k))
		}
		m = press(m, keyRunes(" "))
		if m.cursor != 0 || m.showingDetail {
			t.Errorf("tab %d empty: cursor %d, popup %v", tab, m.cursor, m.showingDetail)
		}
		_ = m.renderModuleList()
	}
}

// A pair of pseudo-versions is ~75 columns by itself. The row must give up the
// path squeeze, then the note, then the old version — never the new version,
// which is what the row is for.
func TestNewVersionSurvivesEvenPseudoVersionsOnNarrowTerminals(t *testing.T) {
	for _, w := range []int{80, 100, 120, 160} {
		m := sized(benchModel(30), w, 40) // benchModel uses pseudo-versions and blocked-newer notes
		view := m.renderModuleList()

		for i, line := range strings.Split(view, "\n")[:m.getViewportHeight()] {
			if lipgloss.Width(line) > w {
				t.Errorf("width %d row %d is %d columns", w, i, lipgloss.Width(line))
			}
			if !strings.Contains(line, "v0.0.0-20251201101010-abcdef123456") {
				t.Errorf("width %d row %d lost the new version: %q", w, i, line)
			}
		}
	}
}

func TestRowDegradesInStagesAsTheTerminalNarrows(t *testing.T) {
	m := sized(semverModel(5), 140, 40)
	m.updateModules[1].Info.BlockedNewer = 2
	if m = sized(m, 140, 40); m.hideNote || m.hideCurrent {
		t.Fatal("a wide terminal should show everything")
	}
	if m = sized(m, 62, 40); !m.hideNote || m.hideCurrent {
		t.Errorf("at 62 columns the note should go first: hideNote=%v hideCurrent=%v", m.hideNote, m.hideCurrent)
	}

	m = sized(benchModel(5), 70, 40) // two pseudo-versions cannot share 70 columns with a path
	if !m.hideCurrent {
		t.Error("when old and new cannot both fit, the old version should be dropped")
	}
}
