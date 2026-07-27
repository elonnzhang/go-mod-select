package ui

import (
	_ "embed"
	"fmt"
	"strings"

	"go-mod-select/pkg/finder"
	"go-mod-select/pkg/parser"

	"github.com/charmbracelet/lipgloss"
)

//go:embed banner.txt
var banner string

func (m AppModel) renderWelcome() string {
	var b strings.Builder

	// Banner
	bannerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("205")).
		Width(m.width).
		Align(lipgloss.Center)

	b.WriteString(bannerStyle.Render(banner))
	b.WriteString("\n")

	// Current info box
	infoBox := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		Padding(1, 2).
		Width(m.width - 4).
		Render(m.renderGoModInfo())

	b.WriteString(lipgloss.Place(m.width, lipgloss.Height(infoBox), lipgloss.Center, lipgloss.Top, infoBox))
	b.WriteString("\n\n")

	// Instructions
	instructions := m.helpStyle().
		Width(m.width).
		Align(lipgloss.Center).
		Render("Press Enter to start analyzing dependencies, or q to quit")

	b.WriteString(instructions)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, b.String())
}

// renderGoModInfo renders the current go.mod information in welcome screen
func (m AppModel) renderGoModInfo() string {
	var b strings.Builder

	b.WriteString(m.infoStyle().Render("Current Module Information") + "\n\n")

	b.WriteString(fmt.Sprintf("Module:           %s\n", m.goMod.Module))
	b.WriteString(fmt.Sprintf("Current Go:       %s\n", m.goMod.GoVersion))
	b.WriteString(fmt.Sprintf("Target Go:        %s\n", m.targetGoVersion))
	b.WriteString(fmt.Sprintf("Direct deps:      %d\n", parser.CountDirectDeps(m.goMod.Requires)))
	b.WriteString(fmt.Sprintf("Indirect deps:    %d\n", parser.CountIndirectDeps(m.goMod.Requires)))

	return b.String()
}

func (m AppModel) renderAnalyzing() string {
	// Calculate layout heights
	// Top section: title + status + progress
	// Bottom section: Analysis Log (fixed position)

	// Top section content
	var topSection strings.Builder

	title := m.titleStyle().
		Width(m.width).
		Align(lipgloss.Center).
		Render(banner)

	topSection.WriteString(title)
	topSection.WriteString("\n\n")

	// Main status with spinner
	status := fmt.Sprintf("%s Checking compatibility with Go %s...\n", m.spinner.View(), m.targetGoVersion)

	// Progress info
	if m.analyzingTotal > 0 {
		progress := fmt.Sprintf("\n📊 Progress: %d/%d packages analyzed", m.analyzingCount, m.analyzingTotal)
		status += progress
	}

	statusBox := lipgloss.NewStyle().
		Width(m.width - 4).
		Align(lipgloss.Center).
		Render(status)

	topSection.WriteString(statusBox)

	// Place top section in the upper half
	topHeight := m.height / 2
	topSectionRendered := lipgloss.NewStyle().
		Width(m.width).
		Height(topHeight).
		Align(lipgloss.Center, lipgloss.Center).
		Render(topSection.String())

	// Bottom section: Fixed position log area
	logSection := m.renderAnalyzingLogs()

	// Combine top and bottom sections
	return lipgloss.JoinVertical(lipgloss.Left, topSectionRendered, logSection)
}

func (m AppModel) renderSelection() string {
	if !m.ready {
		return "Initializing..."
	}

	// The popup replaces the list rather than compositing over it: lipgloss has
	// no true overlay, and interleaving styled runs by hand corrupts ANSI
	// sequences at the seams.
	if m.showingDetail {
		return m.renderDetailPopup()
	}

	return lipgloss.JoinVertical(
		lipgloss.Left,
		m.renderSelectionHeader(),
		m.renderTabs(),
		m.viewport.View(),
		m.renderFooter(),
	)
}

func (m AppModel) renderSelectionHeader() string {
	title := m.titleStyle().
		Width(m.width - 4).
		Align(lipgloss.Center).
		Render("📦 Select Modules to Update")

	colorLegend := lipgloss.NewStyle().
		Width(m.width - 4).
		Align(lipgloss.Center).
		Render(
			m.getVersionStyle(finder.Major).Render("■") + " major  " +
				m.getVersionStyle(finder.Minor).Render("■") + " minor  " +
				m.getVersionStyle(finder.Patch).Render("■") + " patch  " +
				m.getVersionStyle(finder.Prerelease).Render("■") + " pre_release",
		)

	var helpText string
	if m.filterMode {
		helpText = m.helpStyle().
			Width(m.width - 4).
			Align(lipgloss.Center).
			Render(fmt.Sprintf("Filter: %s█ | ESC: cancel | Enter: apply", m.filterInput))
	} else if m.currentTab == tabUpdate {
		hint := fmt.Sprintf(
			"↑↓ move | Space toggle | a all | Tab switch | / filter | Enter update | Selected: %d/%d",
			m.countSelected(), len(m.updateModules))
		if m.filterInput != "" {
			hint = fmt.Sprintf("filter '%s' | ", m.filterInput) + hint
		}
		helpText = m.helpStyle().Width(m.width - 4).Align(lipgloss.Center).Render(hint)
	} else {
		helpText = m.helpStyle().
			Width(m.width - 4).
			Align(lipgloss.Center).
			Render("↑↓ move | Space view details | Tab switch | q quit  (read-only)")
	}

	headerContent := lipgloss.JoinVertical(
		lipgloss.Center,
		title,
		colorLegend,
		helpText,
	)

	return m.headerStyle().
		Width(m.width-2).
		Render(headerContent) + "\n"
}

// renderTabs draws the tab strip with per-tab item counts, so the user can see
// what each tab holds without visiting it.
func (m AppModel) renderTabs() string {
	labels := []struct {
		tab   tabType
		name  string
		count int
	}{
		{tabUpdate, "Update", len(m.updateModules)},
		{tabRequire, "Require", len(m.requireModules)},
		{tabIndirect, "Indirect", len(m.indirectModules)},
		{tabReplace, "Replace", len(m.replaceModules)},
	}

	rendered := make([]string, 0, len(labels))
	for _, l := range labels {
		text := fmt.Sprintf("%s (%d)", l.name, l.count)
		if m.currentTab == l.tab {
			rendered = append(rendered, m.tabActiveStyle().Render(text))
		} else {
			rendered = append(rendered, m.tabInactiveStyle().Render(text))
		}
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, rendered...) + "\n"
}

func (m AppModel) renderUpdating() string {
	// Calculate layout heights - similar to analyzing screen
	// Top section: title + progress
	// Bottom section: Update Log (fixed position)

	// Top section content
	var topSection strings.Builder

	title := m.titleStyle().
		Width(m.width).
		Align(lipgloss.Center).
		Render("⚙️  Updating Modules")

	topSection.WriteString(title)
	topSection.WriteString("\n\n")

	// Progress bar and status
	if m.totalModules > 0 {
		percent := float64(m.currentModule) / float64(m.totalModules)
		progressBar := m.progress.ViewAs(percent)

		status := fmt.Sprintf("Progress: %d/%d modules\n\n", m.currentModule, m.totalModules)
		if m.tidyRunning {
			// The bar stays at 100% while tidy runs, so say what is happening
			// rather than leaving a full bar with no explanation.
			status = fmt.Sprintf("%d/%d modules done — running go mod tidy\n\n",
				m.currentModule, m.totalModules)
		}
		status += progressBar

		statusBox := lipgloss.NewStyle().
			Width(m.width - 4).
			Align(lipgloss.Center).
			Render(status)

		topSection.WriteString(statusBox)
	}

	// Place top section in the upper half
	topHeight := m.height / 2
	topSectionRendered := lipgloss.NewStyle().
		Width(m.width).
		Height(topHeight).
		Align(lipgloss.Center, lipgloss.Center).
		Render(topSection.String())

	// Bottom section: Fixed position log area
	logSection := m.renderUpdateLogs()

	// Combine top and bottom sections
	return lipgloss.JoinVertical(lipgloss.Left, topSectionRendered, logSection)
}

// renderUpdateLogs renders update logs with scrolling
// renderUpdateLogs renders the update log pane, always pinned to the newest
// entry. Its geometry matches renderAnalyzingLogs; see logBoxHeight.
func (m AppModel) renderUpdateLogs() string {
	var b strings.Builder

	b.WriteString(lipgloss.NewStyle().
		Foreground(m.colors.ToLipglossColor(m.colors.Title)).
		Bold(true).
		Render("Update Log"))
	b.WriteString("\n")
	b.WriteString(m.logSeparator())
	b.WriteString("\n")

	visibleLines := m.getAnalyzingLogVisibleLines()
	logCount := len(m.updateLogs)

	if logCount == 0 {
		b.WriteString(m.logStyle().Render("  Starting update..."))
		for i := 1; i < visibleLines; i++ {
			b.WriteString("\n")
		}
		return m.boxLogPane(b.String())
	}

	// The update log always follows the tail; there is no scrolling here
	// because the run is short and finishes on its own.
	start := max(0, logCount-visibleLines)

	rows := make([]string, 0, visibleLines)
	for i := start; i < logCount; i++ {
		rows = append(rows, m.logStyle().Render("  "+m.updateLogs[i]))
	}
	for len(rows) < visibleLines {
		rows = append(rows, "")
	}
	b.WriteString(strings.Join(rows, "\n"))

	if start > 0 {
		b.WriteString("\n" + m.helpStyle().Render(fmt.Sprintf("  ↑ %d earlier", start)))
	}

	return m.boxLogPane(b.String())
}

// boxLogPane gives a log pane its fixed footprint.
func (m AppModel) boxLogPane(content string) string {
	return lipgloss.NewStyle().
		Width(m.width-4).
		Height(m.logBoxHeight()).
		Padding(1, 2).
		Render(content)
}

func (m AppModel) renderComplete() string {
	var b strings.Builder

	heading := "Update Complete!"
	headStyle := m.successStyle()
	if m.updateFailed {
		heading = "Update Failed — changes rolled back"
		headStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(m.colors.ToLipglossColor(m.colors.Prerelease))
	}

	b.WriteString(headStyle.Width(m.width).Align(lipgloss.Center).Render(heading))
	b.WriteString("\n\n")

	// Read the applied set straight off the model. Deriving it by re-parsing
	// log strings used to yield an empty list, because the Updated flag was
	// only set after this screen had already rendered.
	updated := m.updatedModules()

	summary := fmt.Sprintf("Go version:       %s\n", m.targetGoVersion)
	if !m.updateFailed && m.actualGoVersion != "" && !m.goVersionMatched {
		// `go mod tidy` raises the directive when a remaining dependency needs
		// a newer toolchain; say so instead of echoing the request.
		summary = fmt.Sprintf("Go version:       %s  (requested %s)\n",
			m.actualGoVersion, m.targetGoVersion)
	}
	summary += fmt.Sprintf("Modules updated:  %d\n", len(updated))

	if len(updated) > 0 {
		summary += "\nUpdated modules:\n"
		for _, mod := range updated {
			summary += fmt.Sprintf("  • %s: %s → %s\n",
				mod.Info.Path, mod.Info.CurrentVersion, mod.Info.LatestCompatible)
		}
	}

	if m.updateError != nil {
		summary += fmt.Sprintf("\nReason: %v\n", m.updateError)
		summary += "go.mod and go.sum were restored to their previous contents."
	}

	if m.tidySucceeded {
		summary += "\n✓ go mod tidy completed"
	}

	// Only blame tidy when the directive actually ended up *higher* than asked.
	// A lower value means the write did not take effect, which is a different
	// problem and pointing at a dependency would send the user the wrong way.
	if !m.updateFailed && m.actualGoVersion != "" && !m.goVersionMatched {
		if finder.GoVersionSatisfied(m.targetGoVersion, m.actualGoVersion) {
			summary += "\n⚠ go mod tidy raised the go directive: a remaining dependency needs the newer toolchain."
		} else {
			summary += "\n⚠ the go directive ended up lower than requested; the change may not have been applied."
		}
	}

	borderColor := m.colors.Success
	if m.updateFailed {
		borderColor = m.colors.Prerelease
	}

	summaryBox := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(m.colors.ToLipglossColor(borderColor)).
		Padding(1, 2).
		Width(m.width - 4).
		Render(summary)

	b.WriteString(summaryBox)
	b.WriteString("\n\n")

	// Anything still unapplied means another round is possible.
	pending := 0
	for _, mod := range m.updateModules {
		if !mod.Updated {
			pending++
		}
	}

	hint := "All modules updated! Press Enter to review or q to exit"
	if pending > 0 {
		hint = fmt.Sprintf("Press Enter to pick from the remaining %d module(s), or q to exit", pending)
	}

	b.WriteString(m.helpStyle().Width(m.width).Align(lipgloss.Center).Render(hint))

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, b.String())
}

func (m AppModel) renderModuleList() string {
	var lines []string

	switch m.currentTab {
	case tabUpdate:
		visibleModules := m.getVisibleUpdateModules()
		if len(visibleModules) == 0 {
			// Distinguish "filter matched nothing" from "nothing to update":
			// the first is the user's doing, the second is a real result.
			if m.filterInput != "" {
				return m.helpStyle().Render(fmt.Sprintf("  No modules match '%s'", m.filterInput))
			}
			return m.helpStyle().Render(
				fmt.Sprintf("  All direct dependencies are already at their newest Go %s-compatible version", m.targetGoVersion))
		}
		for i, idx := range visibleModules {
			lines = append(lines, m.renderUpdateLine(m.updateModules[idx], i == m.cursor))
		}

	case tabRequire:
		if len(m.requireModules) == 0 {
			return m.helpStyle().Render("  No direct dependencies")
		}
		for i, req := range m.requireModules {
			lines = append(lines, m.renderRequireLine(req, i == m.cursor))
		}

	case tabIndirect:
		if len(m.indirectModules) == 0 {
			return m.helpStyle().Render("  No indirect dependencies")
		}
		for i, req := range m.indirectModules {
			lines = append(lines, m.renderRequireLine(req, i == m.cursor))
		}

	case tabReplace:
		if len(m.replaceModules) == 0 {
			return m.helpStyle().Render("  No replace directives")
		}
		for i, rep := range m.replaceModules {
			lines = append(lines, m.renderReplaceLine(rep, i == m.cursor))
		}
	}

	return strings.Join(lines, "\n")
}

// pathColumnWidth is how much of a module path is shown before eliding. Paths
// are truncated from the left so the distinguishing tail stays readable —
// "…/charmbracelet/bubbletea" tells you more than "github.com/charmbrace…".
const pathColumnWidth = 50

func elidePath(path string) string {
	if len(path) <= pathColumnWidth {
		return path
	}
	return "…" + path[len(path)-(pathColumnWidth-1):]
}

// renderUpdateLine renders a line for Update tab (with checkbox and version changes)
func (m AppModel) renderUpdateLine(mod *SelectableModule, isCursor bool) string {
	modulePath := elidePath(mod.Info.Path)

	var checkbox, versions string
	if mod.Updated {
		checkbox = "[" + m.successStyle().Render("✔") + "]"
		versions = m.successStyle().Render(mod.Info.LatestCompatible)
	} else {
		checkbox = "[ ]"
		if mod.Selected {
			checkbox = "[" + m.selectedStyle().Render("✓") + "]"
		}

		arrow := "→"
		if mod.Info.Downgrade {
			// A downgrade is the honest outcome when the pinned version needs
			// a newer Go than the target; flag it rather than let it read as
			// an ordinary update.
			arrow = "↓"
		}
		versions = fmt.Sprintf("%s %s %s",
			lipgloss.NewStyle().Faint(true).Render(mod.Info.CurrentVersion),
			arrow,
			m.getVersionStyle(mod.Info.UpdateType).Render(mod.Info.LatestCompatible),
		)

		// Answer "why not the latest release?" on the row itself, instead of
		// leaving the count only in the log file.
		if n := mod.Info.BlockedNewer; n > 0 {
			versions += m.helpStyle().Render(fmt.Sprintf("  (%d newer need a later Go)", n))
		}
	}

	line := fmt.Sprintf("%s  %-*s  %s", checkbox, pathColumnWidth, modulePath, versions)
	if mod.Updated {
		line = lipgloss.NewStyle().Faint(true).Render(line)
	}

	if isCursor {
		return m.cursorStyle().Render("▶ ") + line
	}
	return "  " + line
}

// renderRequireLine renders a read-only dependency row. Direct and indirect
// requires render identically, so they share one function.
func (m AppModel) renderRequireLine(req parser.Require, isCursor bool) string {
	line := fmt.Sprintf("    %-*s  %s", pathColumnWidth, elidePath(req.Path), req.Version)

	if isCursor {
		return m.cursorStyle().Render("▶") + line
	}
	return " " + line
}

func (m AppModel) renderReplaceLine(rep ReplaceInfo, isCursor bool) string {
	line := fmt.Sprintf("    %s => %s", rep.Old, rep.New)

	if isCursor {
		line = m.cursorStyle().Render("▶") + line
	} else {
		line = " " + line
	}

	return line
}

func (m AppModel) renderFooter() string {
	separator := lipgloss.NewStyle().
		Width(m.width).
		Foreground(lipgloss.Color("238")).
		Render(strings.Repeat("─", m.width))

	logTitle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("99")).
		Padding(0, 1).
		Render("Activity Log")

	var logLines []string
	if len(m.operationLogs) == 0 {
		logLines = []string{
			m.logStyle().Render("  No activity yet"),
			"",
			"",
		}
	} else {
		for _, log := range m.operationLogs {
			logLines = append(logLines, m.logStyle().Render("  • "+log))
		}
		for len(logLines) < 3 {
			logLines = append(logLines, "")
		}
	}

	logs := strings.Join(logLines, "\n")

	return lipgloss.JoinVertical(
		lipgloss.Left,
		separator,
		logTitle,
		logs,
	)
}

// popupWidth is the outer width of the detail popup.
func (m AppModel) popupWidth() int {
	return max(min(m.width-10, 80), 24)
}

// detailViewportSize is the inner size of the popup's scrollable area: outer
// width less border (2) and horizontal padding (4), outer height less border
// (2), vertical padding (2), and the help line below the box (1).
func (m AppModel) detailViewportSize() (width, height int) {
	outerHeight := min(m.height-10, 30)
	return max(m.popupWidth()-6, 20), max(outerHeight-5, 3)
}

func (m AppModel) renderDetailPopup() string {
	styledContent := lipgloss.NewStyle().
		Padding(1, 2).
		Render(m.detailViewport.View())

	popup := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(m.colors.ToLipglossColor(m.colors.PopupBorder)).
		Width(m.popupWidth()).
		Render(styledContent)

	help := "↑↓ scroll | ESC close"
	if m.detailViewport.TotalLineCount() > m.detailViewport.Height {
		help = fmt.Sprintf("↑↓ scroll (%d%%) | PgUp/PgDn | ESC close",
			int(m.detailViewport.ScrollPercent()*100))
	}

	popupWithHelp := lipgloss.JoinVertical(lipgloss.Left, popup, m.helpStyle().Render(help))

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, popupWithHelp)
}

// renderAnalyzingLogs renders a fixed-height log pane whose visible window is
// driven by analyzingLogOffset, so the ↑/↓ keys on the analyzing screen
// actually move the view.
func (m AppModel) renderAnalyzingLogs() string {
	var b strings.Builder

	logTitle := lipgloss.NewStyle().
		Foreground(m.colors.ToLipglossColor(m.colors.Title)).
		Bold(true).
		Render("📝 Analysis Log")

	b.WriteString(logTitle)
	b.WriteString("\n")
	b.WriteString(m.logSeparator())
	b.WriteString("\n")

	visibleLines := m.getAnalyzingLogVisibleLines()
	logCount := len(m.analysisLogs)

	if logCount == 0 {
		b.WriteString(m.logStyle().Render("  Waiting for analysis to start..."))
		for i := 1; i < visibleLines; i++ {
			b.WriteString("\n")
		}
	} else {
		// Clamp here as well as on keypress: the log grows underneath us, so
		// an offset valid a moment ago can now point past the end.
		start := max(0, min(m.analyzingLogOffset, max(0, logCount-visibleLines)))
		end := min(logCount, start+visibleLines)

		// Rows are joined, not newline-terminated: a trailing "\n" would add an
		// extra line and push the whole screen one row over the terminal height.
		rows := make([]string, 0, visibleLines)
		for i := start; i < end; i++ {
			rows = append(rows, m.formatAnalysisLogLine(m.analysisLogs[i]))
		}
		for len(rows) < visibleLines {
			rows = append(rows, "")
		}
		b.WriteString(strings.Join(rows, "\n"))

		// Position indicator, so a scrolled-up user knows where they are.
		if logCount > visibleLines {
			pos := fmt.Sprintf("  %d-%d of %d", start+1, end, logCount)
			if !m.analyzingLogFollow {
				pos += "  (↓/G to follow)"
			}
			b.WriteString("\n" + m.helpStyle().Render(pos))
		}
	}

	return lipgloss.NewStyle().
		Width(m.width-4).
		Height(m.logBoxHeight()).
		Padding(1, 2).
		Render(b.String())
}

// logSeparator is the horizontal rule under a log pane's title.
func (m AppModel) logSeparator() string {
	width := m.width - 10
	if width < 1 {
		width = 1
	}
	return lipgloss.NewStyle().
		Foreground(m.colors.ToLipglossColor(m.colors.Border)).
		Render(strings.Repeat("─", width))
}

// analysisLogStyles maps a status tag to its label and color, keeping the
// formatter a single table lookup instead of a prefix-matching chain.
func (m AppModel) analysisLogStyles() map[string]struct {
	label string
	color string
	bold  bool
	faint bool
} {
	return map[string]struct {
		label string
		color string
		bold  bool
		faint bool
	}{
		statusChecking: {"⏳ Checking", m.colors.Info, false, false},
		statusFound:    {"✓ Found   ", m.colors.Success, true, false},
		statusSkipped:  {"- Skipped ", m.colors.Help, false, true},
		statusError:    {"✗ Error   ", m.colors.Prerelease, true, false},
	}
}

// formatAnalysisLogLine renders a stored "[STATUS]pkg" entry with color.
func (m AppModel) formatAnalysisLogLine(logLine string) string {
	for status, style := range m.analysisLogStyles() {
		prefix := logPrefix(status)
		if !strings.HasPrefix(logLine, prefix) {
			continue
		}

		pkg := strings.TrimPrefix(logLine, prefix)
		statusText := lipgloss.NewStyle().
			Foreground(m.colors.ToLipglossColor(style.color)).
			Bold(style.bold).
			Render(style.label)

		pkgStyle := m.logStyle()
		if style.faint {
			pkgStyle = lipgloss.NewStyle().Faint(true)
		}

		return "  " + statusText + " " + pkgStyle.Render(pkg)
	}

	return "  " + m.logStyle().Render(logLine)
}
