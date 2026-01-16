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

func (m AppModel) renderGoModInfo() string {
	var b strings.Builder

	b.WriteString(m.infoStyle().Render("Current Module Information") + "\n\n")

	b.WriteString(fmt.Sprintf("Module:           %s\n", m.goMod.Module))
	b.WriteString(fmt.Sprintf("Current Go:       %s\n", m.goMod.GoVersion))
	b.WriteString(fmt.Sprintf("Target Go:        %s\n", m.targetGoVersion))
	b.WriteString(fmt.Sprintf("Direct deps:      %d\n", countDirectDeps(m.goMod.Requires)))
	b.WriteString(fmt.Sprintf("Indirect deps:    %d\n", countIndirectDeps(m.goMod.Requires)))

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

	header := m.renderSelectionHeader()
	tabs := m.renderTabs()
	viewportSection := m.viewport.View()
	footer := m.renderFooter()

	baseView := lipgloss.JoinVertical(lipgloss.Left, header, tabs, viewportSection, footer)

	// If showing detail popup, overlay it on top of the base view
	if m.showingDetail {
		return m.renderDetailPopup(baseView)
	}

	return baseView
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
	} else {
		selectedCount := m.countSelected()
		canSelectText := ""
		spaceAction := "toggle"
		if m.currentTab != tabUpdate {
			canSelectText = " (Read-only)"
			spaceAction = "view details"
		}
		helpText = m.helpStyle().
			Width(m.width - 4).
			Align(lipgloss.Center).
			Render(fmt.Sprintf(
				"↑↓: move | Space: %s | Tab: switch | /: filter | Enter: confirm%s | Selected: %d",
				spaceAction, canSelectText, selectedCount,
			))
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

func (m AppModel) renderTabs() string {
	tabs := []string{}

	// Update tab (can select)
	updateText := "Update"
	if m.currentTab == tabUpdate {
		updateText = m.tabActiveStyle().Render(updateText)
	} else {
		updateText = m.tabInactiveStyle().Render(updateText)
	}
	tabs = append(tabs, updateText)

	// Require tab (read-only)
	requireText := "Require"
	if m.currentTab == tabRequire {
		requireText = m.tabActiveStyle().Render(requireText)
	} else {
		requireText = m.tabInactiveStyle().Render(requireText)
	}
	tabs = append(tabs, requireText)

	// Indirect tab (read-only)
	indirectText := "Indirect"
	if m.currentTab == tabIndirect {
		indirectText = m.tabActiveStyle().Render(indirectText)
	} else {
		indirectText = m.tabInactiveStyle().Render(indirectText)
	}
	tabs = append(tabs, indirectText)

	// Replace tab (read-only)
	replaceText := "Replace"
	if m.currentTab == tabReplace {
		replaceText = m.tabActiveStyle().Render(replaceText)
	} else {
		replaceText = m.tabInactiveStyle().Render(replaceText)
	}
	tabs = append(tabs, replaceText)

	return lipgloss.JoinHorizontal(lipgloss.Top, tabs...) + "\n"
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
func (m AppModel) renderUpdateLogs() string {
	// Calculate fixed height for log area (bottom half of screen)
	logBoxHeight := m.height / 2

	// Ensure minimum height
	if logBoxHeight < 10 {
		logBoxHeight = 10
	}

	var b strings.Builder

	// Fixed title and separator
	logTitle := lipgloss.NewStyle().
		Foreground(m.colors.ToLipglossColor(m.colors.Title)).
		Bold(true).
		Render("Update Log")

	b.WriteString(logTitle)
	b.WriteString("\n")

	// Separator
	separatorWidth := m.width - 4 - 6
	if separatorWidth < 1 {
		separatorWidth = 1
	}
	separator := lipgloss.NewStyle().
		Foreground(m.colors.ToLipglossColor(m.colors.Border)).
		Render(strings.Repeat("─", separatorWidth))
	b.WriteString(separator)
	b.WriteString("\n")

	// Calculate available lines for scrolling log content
	logContentHeight := logBoxHeight - 4
	if logContentHeight < 3 {
		logContentHeight = 3
	}

	// Render scrolling log content
	logCount := len(m.updateLogs)
	if logCount == 0 {
		// Show placeholder
		b.WriteString(m.logStyle().Render("  Starting update..."))
		b.WriteString("\n")
		// Fill remaining space
		for i := 1; i < logContentHeight; i++ {
			b.WriteString("\n")
		}
	} else {
		// Calculate which logs to show (auto-scroll to bottom)
		startIdx := 0
		if logCount > logContentHeight {
			startIdx = logCount - logContentHeight
		}

		// Show scroll indicator if there are hidden logs above
		linesToRender := 0
		if startIdx > 0 {
			indicator := m.helpStyle().Render(fmt.Sprintf("  ↑ %d more ↑", startIdx))
			b.WriteString(indicator)
			b.WriteString("\n")
			linesToRender++
		}

		// Render visible logs
		for i := startIdx; i < logCount && linesToRender < logContentHeight; i++ {
			logLine := m.logStyle().Render("  " + m.updateLogs[i])
			b.WriteString(logLine)
			b.WriteString("\n")
			linesToRender++
		}

		// Fill remaining space with empty lines
		for linesToRender < logContentHeight {
			b.WriteString("\n")
			linesToRender++
		}
	}

	// Box the log section with fixed height
	logBox := lipgloss.NewStyle().
		Width(m.width-4).
		Height(logBoxHeight).
		Padding(1, 2).
		Render(b.String())

	return logBox
}

func (m AppModel) renderComplete() string {
	var b strings.Builder

	title := m.successStyle().
		Width(m.width).
		Align(lipgloss.Center).
		Render("Update Complete!")

	b.WriteString(title)
	b.WriteString("\n\n")

	// Collect successfully updated modules (exclude tidy operations)
	var updatedModules []string
	for _, log := range m.updateLogs {
		if strings.Contains(log, "updated successfully") && !strings.Contains(log, "go mod tidy") {
			// Extract module name from log like "✓ [1/3] github.com/example/module updated successfully"
			parts := strings.Fields(log)
			if len(parts) >= 3 {
				// Find the module name (between [X/Y] and "updated")
				for i, part := range parts {
					if strings.HasPrefix(part, "[") && strings.Contains(part, "/") {
						if i+1 < len(parts) {
							moduleName := parts[i+1]
							updatedModules = append(updatedModules, moduleName)
							break
						}
					}
				}
			}
		}
	}

	// Summary
	summary := fmt.Sprintf("Go version: %s\n", m.targetGoVersion)
	summary += fmt.Sprintf("Modules updated: %d\n\n", len(updatedModules))

	// Updated modules list
	if len(updatedModules) > 0 {
		summary += "Updated modules:\n"
		for _, moduleName := range updatedModules {
			// Find the module info to get version details
			for _, mod := range m.updateModules {
				if mod.Info.Path == moduleName && mod.Updated {
					summary += fmt.Sprintf("  • %s: %s → %s\n",
						mod.Info.Path,
						mod.Info.CurrentVersion,
						mod.Info.LatestCompatible)
					break
				}
			}
		}
	}

	// Add tidy status
	tidySuccess := false
	for _, log := range m.updateLogs {
		if strings.Contains(log, "go mod tidy") && strings.Contains(log, "✓") {
			tidySuccess = true
			break
		}
	}
	if tidySuccess {
		summary += "\n✓ go mod tidy completed"
	}

	summaryBox := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(m.colors.ToLipglossColor(m.colors.Success)).
		Padding(1, 2).
		Width(m.width - 4).
		Render(summary)

	b.WriteString(lipgloss.Place(m.width, 0, lipgloss.Center, lipgloss.Top, summaryBox))
	b.WriteString("\n\n")

	// Check if there are more modules to update
	hasMoreToUpdate := false
	for _, mod := range m.updateModules {
		if !mod.Updated {
			hasMoreToUpdate = true
			break
		}
	}

	var help string
	if hasMoreToUpdate {
		help = m.helpStyle().
			Width(m.width).
			Align(lipgloss.Center).
			Render("Press Enter to continue updating more modules, or q to exit")
	} else {
		help = m.helpStyle().
			Width(m.width).
			Align(lipgloss.Center).
			Render("All modules updated! Press Enter to review or q to exit")
	}

	b.WriteString(help)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, b.String())
}

func (m AppModel) renderModuleList() string {
	var lines []string

	switch m.currentTab {
	case tabUpdate:
		// Show modules with available updates (can select)
		visibleModules := m.getVisibleUpdateModules()
		if len(visibleModules) == 0 {
			return m.helpStyle().Render("No modules match the filter")
		}
		for i, idx := range visibleModules {
			mod := m.updateModules[idx]
			line := m.renderUpdateLine(mod, i == m.cursor)
			lines = append(lines, line)
		}

	case tabRequire:
		// Show current require directives (read-only)
		if len(m.requireModules) == 0 {
			return m.helpStyle().Render("No require directives")
		}
		for i, req := range m.requireModules {
			line := m.renderRequireInfoLine(req, i == m.cursor)
			lines = append(lines, line)
		}

	case tabIndirect:
		// Show current indirect requires (read-only)
		if len(m.indirectModules) == 0 {
			return m.helpStyle().Render("No indirect requires")
		}
		for i, req := range m.indirectModules {
			line := m.renderIndirectInfoLine(req, i == m.cursor)
			lines = append(lines, line)
		}

	case tabReplace:
		// Show current replace directives (read-only)
		if len(m.replaceModules) == 0 {
			return m.helpStyle().Render("No replace directives")
		}
		for i, rep := range m.replaceModules {
			line := m.renderReplaceLine(rep, i == m.cursor)
			lines = append(lines, line)
		}
	}

	return strings.Join(lines, "\n")
}

// renderUpdateLine renders a line for Update tab (with checkbox and version changes)
func (m AppModel) renderUpdateLine(mod *SelectableModule, isCursor bool) string {
	var checkbox string
	var line string

	modulePath := mod.Info.Path
	if len(modulePath) > 50 {
		modulePath = modulePath[:47] + "..."
	}

	if mod.Updated {
		// Already updated - show as completed
		checkbox = "[" + m.successStyle().Render("✔") + "]"

		// Show updated version with success color
		versions := fmt.Sprintf("%s",
			m.successStyle().Render(mod.Info.LatestCompatible),
		)

		line = fmt.Sprintf("%s  %-50s  %s", checkbox, modulePath, versions)

		// Make the whole line faint to indicate it's done
		line = lipgloss.NewStyle().Faint(true).Render(line)
	} else {
		// Not updated yet - show as selectable
		checkbox = "[ ]"
		if mod.Selected {
			checkbox = "[" + m.selectedStyle().Render("✓") + "]"
		}

		versionColor := m.getVersionStyle(mod.Info.UpdateType)
		versions := fmt.Sprintf("%s → %s",
			lipgloss.NewStyle().Faint(true).Render(mod.Info.CurrentVersion),
			versionColor.Render(mod.Info.LatestCompatible),
		)

		line = fmt.Sprintf("%s  %-50s  %s", checkbox, modulePath, versions)
	}

	if isCursor {
		line = m.cursorStyle().Render("▶ ") + line
	} else {
		line = "  " + line
	}

	return line
}

// renderRequireInfoLine renders a line for Require tab (read-only, current version only)
func (m AppModel) renderRequireInfoLine(req parser.Require, isCursor bool) string {
	modulePath := req.Path
	if len(modulePath) > 50 {
		modulePath = modulePath[:47] + "..."
	}

	line := fmt.Sprintf("    %-50s  %s", modulePath, req.Version)

	if isCursor {
		line = m.cursorStyle().Render("▶") + line
	} else {
		line = " " + line
	}

	return line
}

// renderIndirectInfoLine renders a line for Indirect tab (read-only, current version only)
func (m AppModel) renderIndirectInfoLine(req parser.Require, isCursor bool) string {
	modulePath := req.Path
	if len(modulePath) > 50 {
		modulePath = modulePath[:47] + "..."
	}

	line := fmt.Sprintf("    %-50s  %s", modulePath, req.Version)

	if isCursor {
		line = m.cursorStyle().Render("▶") + line
	} else {
		line = " " + line
	}

	return line
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

func (m AppModel) renderDetailPopup(baseView string) string {
	// Create popup box with scrollable detail content
	popupWidth := min(m.width-10, 80)

	// Render viewport content with padding
	viewportContent := m.detailViewport.View()

	// Wrap viewport content with padding style
	styledContent := lipgloss.NewStyle().
		Padding(1, 2).
		Render(viewportContent)

	// Create bordered popup
	popup := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(m.colors.ToLipglossColor(m.colors.PopupBorder)).
		Width(popupWidth).
		Render(styledContent)

	// Add help text at bottom of popup
	helpText := m.helpStyle().Render("↑↓: scroll | ESC: close")
	popupWithHelp := lipgloss.JoinVertical(lipgloss.Left, popup, helpText)

	// Overlay popup on center of base view
	// Use lipgloss.Place to center the popup
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, popupWithHelp)
}

// renderAnalyzingLogs renders a fixed-position log section with scrolling content
func (m AppModel) renderAnalyzingLogs() string {
	// Calculate fixed height for log area (bottom half of screen)
	logBoxHeight := m.height / 2

	// Ensure minimum height
	if logBoxHeight < 10 {
		logBoxHeight = 10
	}

	var b strings.Builder

	// Fixed title and separator (these don't scroll)
	logTitle := lipgloss.NewStyle().
		Foreground(m.colors.ToLipglossColor(m.colors.Title)).
		Bold(true).
		Render("📝 Analysis Log")

	b.WriteString(logTitle)
	b.WriteString("\n")

	// Separator
	separatorWidth := m.width - 4 - 6
	if separatorWidth < 1 {
		separatorWidth = 1
	}
	separator := lipgloss.NewStyle().
		Foreground(m.colors.ToLipglossColor(m.colors.Border)).
		Render(strings.Repeat("─", separatorWidth))
	b.WriteString(separator)
	b.WriteString("\n")

	// Calculate available lines for scrolling log content
	// Box has: title(1) + separator(1) + padding(2 top+bottom) = 4 lines overhead
	logContentHeight := logBoxHeight - 4
	if logContentHeight < 3 {
		logContentHeight = 3
	}

	// Render scrolling log content
	logCount := len(m.analysisLogs)
	if logCount == 0 {
		// Show placeholder
		b.WriteString(m.logStyle().Render("  Waiting for analysis to start..."))
		b.WriteString("\n")
		// Fill remaining space with empty lines to maintain fixed height
		for i := 1; i < logContentHeight; i++ {
			b.WriteString("\n")
		}
	} else {
		// Calculate which logs to show (auto-scroll to bottom)
		startIdx := 0
		if logCount > logContentHeight {
			startIdx = logCount - logContentHeight
		}

		// Show scroll indicator if there are hidden logs above
		linesToRender := 0
		if startIdx > 0 {
			indicator := m.helpStyle().Render(fmt.Sprintf("  ↑ %d more ↑", startIdx))
			b.WriteString(indicator)
			b.WriteString("\n")
			linesToRender++
		}

		// Render visible logs
		for i := startIdx; i < logCount && linesToRender < logContentHeight; i++ {
			logLine := m.formatAnalysisLogLine(m.analysisLogs[i])
			b.WriteString(logLine)
			b.WriteString("\n")
			linesToRender++
		}

		// Fill remaining space with empty lines to maintain fixed height
		for linesToRender < logContentHeight {
			b.WriteString("\n")
			linesToRender++
		}
	}

	// Box the log section with fixed height
	logBox := lipgloss.NewStyle().
		Width(m.width-4).
		Height(logBoxHeight).
		Padding(1, 2).
		Render(b.String())

	return logBox
}

// formatAnalysisLogLine formats a log line with color based on status
func (m AppModel) formatAnalysisLogLine(logLine string) string {
	// Parse status tag and apply appropriate color
	if strings.HasPrefix(logLine, "[CHECKING]") {
		packageName := strings.TrimPrefix(logLine, "[CHECKING]")
		statusStyle := lipgloss.NewStyle().
			Foreground(m.colors.ToLipglossColor(m.colors.Info)).
			Render("⏳ Checking")
		return "  " + statusStyle + " " + m.logStyle().Render(packageName)
	} else if strings.HasPrefix(logLine, "[FOUND]") {
		packageName := strings.TrimPrefix(logLine, "[FOUND]")
		statusStyle := lipgloss.NewStyle().
			Foreground(m.colors.ToLipglossColor(m.colors.Success)).
			Bold(true).
			Render("✓ Found")
		return "  " + statusStyle + " " + m.logStyle().Render(packageName)
	} else if strings.HasPrefix(logLine, "[SKIPPED]") {
		packageName := strings.TrimPrefix(logLine, "[SKIPPED]")
		statusStyle := lipgloss.NewStyle().
			Foreground(m.colors.ToLipglossColor(m.colors.Help)).
			Render("- Skipped")
		return "  " + statusStyle + " " + lipgloss.NewStyle().Faint(true).Render(packageName)
	} else if strings.HasPrefix(logLine, "[ERROR]") {
		packageName := strings.TrimPrefix(logLine, "[ERROR]")
		statusStyle := lipgloss.NewStyle().
			Foreground(m.colors.ToLipglossColor(m.colors.Prerelease)).
			Bold(true).
			Render("✗ Error")
		return "  " + statusStyle + " " + m.logStyle().Render(packageName)
	}

	// Fallback for any unrecognized format
	return "  " + m.logStyle().Render(logLine)
}

func countDirectDeps(requires []parser.Require) int {
	count := 0
	for _, req := range requires {
		if !req.Indirect {
			count++
		}
	}
	return count
}

func countIndirectDeps(requires []parser.Require) int {
	count := 0
	for _, req := range requires {
		if req.Indirect {
			count++
		}
	}
	return count
}
