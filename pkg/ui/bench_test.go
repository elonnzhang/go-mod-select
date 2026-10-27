package ui

import (
	"fmt"
	"testing"

	"github.com/elonnzhang/go-mod-select/pkg/config"
	"github.com/elonnzhang/go-mod-select/pkg/finder"
	"github.com/elonnzhang/go-mod-select/pkg/parser"

	tea "github.com/charmbracelet/bubbletea"
)

// benchModel builds a selection-screen model with n updatable modules, sized
// like a typical terminal. Versions are pseudo-versions, the widest realistic
// row content.
func benchModel(n int) AppModel {
	colors := config.DefaultColorConfig()
	m := AppModel{
		currentScreen:   screenSelection,
		targetGoVersion: "1.25",
		colors:          colors,
		styles:          newStyleSet(colors),
		width:           120,
		height:          40,
		currentTab:      tabUpdate,
		goMod:           &parser.GoMod{Module: "example.com/x"},
	}
	for i := 0; i < n; i++ {
		m.updateModules = append(m.updateModules, &SelectableModule{
			Info: &finder.ModuleInfo{
				Path:             fmt.Sprintf("github.com/org%d/some-module-%d", i%17, i),
				CurrentVersion:   "v0.0.0-20250311204145-2c3ea96c31dd",
				LatestCompatible: "v0.0.0-20251201101010-abcdef123456",
				UpdateType:       finder.Minor,
				BlockedNewer:     i % 4,
			},
		})
	}
	return m
}

func readyBenchModel(n int) AppModel {
	m := benchModel(n)
	m.fitColumns()
	return m
}

// BenchmarkCursorDown is the cost of one ↓ keypress on the Update tab.
func BenchmarkCursorDown(b *testing.B) {
	for _, n := range []int{50, 300} {
		b.Run(fmt.Sprintf("modules=%d", n), func(b *testing.B) {
			m := readyBenchModel(n)
			down := tea.KeyMsg{Type: tea.KeyDown}
			up := tea.KeyMsg{Type: tea.KeyUp}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				msg := down
				if i%2 == 1 {
					msg = up
				}
				model, _ := m.handleKeyPress(msg)
				m = model.(AppModel)
				_ = m.View()
			}
		})
	}
}

// BenchmarkSpinnerFrame is the cost of one idle redraw of the selection screen,
// which is what every spinner tick triggers.
func BenchmarkSpinnerFrame(b *testing.B) {
	m := readyBenchModel(300)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}

// BenchmarkAnalysisLogFrame is one redraw of the analyzing screen with a full
// log pane.
func BenchmarkAnalysisLogFrame(b *testing.B) {
	m := benchModel(0)
	m.currentScreen = screenAnalyzing
	for i := 0; i < 300; i++ {
		status := []string{statusFound, statusSkipped, statusError, statusChecking}[i%4]
		m.analysisLogs = append(m.analysisLogs, logPrefix(status)+fmt.Sprintf("github.com/org/mod-%d", i))
	}
	m.analyzingLogFollow = true
	m.analyzingLogOffset = m.maxAnalyzingLogOffset()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}

// BenchmarkCursorDownFiltered is one ↓ keypress with a filter applied, where
// every call to visibleIndices lowercases and matches each module path.
func BenchmarkCursorDownFiltered(b *testing.B) {
	m := readyBenchModel(300)
	m.filterInput = "module-1"
	down, up := tea.KeyMsg{Type: tea.KeyDown}, tea.KeyMsg{Type: tea.KeyUp}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg := down
		if i%2 == 1 {
			msg = up
		}
		model, _ := m.handleKeyPress(msg)
		m = model.(AppModel)
		_ = m.View()
	}
}
