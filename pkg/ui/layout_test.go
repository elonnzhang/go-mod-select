package ui

import (
	"strconv"
	"testing"

	"github.com/elonnzhang/go-mod-select/pkg/config"
	"github.com/elonnzhang/go-mod-select/pkg/parser"

	"github.com/charmbracelet/lipgloss"
)

// TestFullScreenNeverExceedsTerminalHeight guards a layout bug: the log panes
// newline-terminated their rows, so the joined screen came to height+1 lines and
// bubbletea's renderer dropped the topmost line, cutting the banner.
func TestFullScreenNeverExceedsTerminalHeight(t *testing.T) {
	for _, h := range []int{20, 24, 25, 30, 31, 40, 41, 50, 51} {
		m := AppModel{
			currentScreen:   screenAnalyzing,
			targetGoVersion: "1.25",
			colors:          config.DefaultColorConfig(),
			width:           120,
			height:          h,
			goMod:           &parser.GoMod{Module: "example.com/x"},
		}
		// More logs than fit, so the status line and scroll indicator render.
		for i := 0; i < 200; i++ {
			m.analysisLogs = append(m.analysisLogs, logPrefix(statusFound)+"mod-"+strconv.Itoa(i))
			m.updateLogs = append(m.updateLogs, "✓ ["+strconv.Itoa(i)+"/200] mod updated successfully")
		}
		m.analyzingLogOffset = 5
		m.analyzingLogFollow = false

		if got := lipgloss.Height(m.renderAnalyzing()); got > h {
			t.Errorf("analyzing screen at height %d rendered %d lines", h, got)
		}

		m.currentScreen = screenUpdating
		m.totalModules = 200
		m.currentModule = 42
		if got := lipgloss.Height(m.renderUpdating()); got > h {
			t.Errorf("updating screen at height %d rendered %d lines", h, got)
		}
	}
}

func TestLogPanesFitWhenEmpty(t *testing.T) {
	m := AppModel{
		currentScreen:   screenAnalyzing,
		targetGoVersion: "1.25",
		colors:          config.DefaultColorConfig(),
		width:           100,
		height:          30,
		goMod:           &parser.GoMod{Module: "example.com/x"},
	}

	if got := lipgloss.Height(m.renderAnalyzing()); got > 30 {
		t.Errorf("empty analyzing screen rendered %d lines, want <= 30", got)
	}

	m.currentScreen = screenUpdating
	m.totalModules = 1
	if got := lipgloss.Height(m.renderUpdating()); got > 30 {
		t.Errorf("empty updating screen rendered %d lines, want <= 30", got)
	}
}

// TestTinyTerminalDoesNotPanic covers the clamped-minimum paths.
func TestTinyTerminalDoesNotPanic(t *testing.T) {
	m := AppModel{
		currentScreen:   screenAnalyzing,
		targetGoVersion: "1.25",
		colors:          config.DefaultColorConfig(),
		width:           8,
		height:          4,
		goMod:           &parser.GoMod{Module: "x"},
		analysisLogs:    []string{logPrefix(statusError) + "m"},
	}

	_ = m.renderAnalyzing()

	m.currentScreen = screenUpdating
	m.updateLogs = []string{"line"}
	_ = m.renderUpdating()
}
