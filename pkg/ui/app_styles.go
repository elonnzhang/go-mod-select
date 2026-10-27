package ui

import (
	"github.com/elonnzhang/go-mod-select/pkg/config"
	"github.com/elonnzhang/go-mod-select/pkg/finder"

	"github.com/charmbracelet/lipgloss"
)

// analysisStyle is how one analysis status is drawn in the log pane.
type analysisStyle struct {
	label string
	text  lipgloss.Style
	pkg   lipgloss.Style
}

// styleSet holds every style the screens reuse, built once from the theme.
//
// Building a lipgloss style is cheap, but doing it per row per frame was not:
// the list and log panes redraw on every key press and spinner tick, and each
// redraw used to allocate hundreds of throwaway styles.
type styleSet struct {
	title, header, tabActive, tabInactive lipgloss.Style
	help, cursor, selected, info          lipgloss.Style
	success, log, failure, faint          lipgloss.Style
	logTitle, separator                   lipgloss.Style

	// version is indexed by finder.UpdateType.
	version [finder.Prerelease + 1]lipgloss.Style

	// analysis is keyed by status (statusChecking, statusFound, ...).
	analysis map[string]analysisStyle
}

func newStyleSet(c *config.ColorConfig) *styleSet {
	fg := func(hex string) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(c.ToLipglossColor(hex))
	}

	s := &styleSet{
		title: fg(c.Title).Bold(true).Padding(0, 1),
		header: lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(c.ToLipglossColor(c.Border)).
			Padding(0, 1).
			Bold(true),
		tabActive: fg(c.TabActive).
			Bold(true).
			Background(c.ToLipglossColor(c.TabBackground)).
			Padding(0, 2),
		tabInactive: fg(c.TabInactive).Padding(0, 2),
		help:        fg(c.Help),
		cursor:      fg(c.Cursor),
		selected:    fg(c.Selected).Bold(true),
		info:        fg(c.Info).Bold(true),
		success:     fg(c.Success).Bold(true),
		log:         fg(c.Log),
		failure:     fg(c.Prerelease).Bold(true),
		faint:       lipgloss.NewStyle().Faint(true),
		logTitle:    fg(c.Title).Bold(true),
		separator:   fg(c.Border),
	}

	s.version[finder.Patch] = fg(c.Patch)
	s.version[finder.Minor] = fg(c.Minor)
	s.version[finder.Major] = fg(c.Major)
	s.version[finder.Prerelease] = fg(c.Prerelease)

	s.analysis = map[string]analysisStyle{
		statusChecking: {"⏳ Checking", fg(c.Info), s.log},
		statusFound:    {"✓ Found   ", fg(c.Success).Bold(true), s.log},
		statusSkipped:  {"- Skipped ", fg(c.Help), s.faint},
		statusError:    {"✗ Error   ", fg(c.Prerelease).Bold(true), s.log},
	}

	return s
}

// st returns the model's styles. Models built without InitialAppModel (tests)
// get a set made on the spot.
func (m AppModel) st() *styleSet {
	if m.styles != nil {
		return m.styles
	}
	return newStyleSet(m.colors)
}
