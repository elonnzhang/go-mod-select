package ui

import (
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/elonnzhang/go-mod-select/pkg/finder"
	"github.com/elonnzhang/go-mod-select/pkg/parser"
	"github.com/elonnzhang/go-mod-select/pkg/updater"

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

	pre := "excluded (--pre to include)"
	if m.policy.AllowPrerelease {
		pre = "included"
	}
	b.WriteString(fmt.Sprintf("Prereleases:      %s\n", pre))
	limit := "none"
	if m.policy.MaxUpdate != finder.LimitMajor {
		limit = m.policy.MaxUpdate.String() + " (--max-update)"
	}
	b.WriteString(fmt.Sprintf("Update limit:     %s\n", limit))

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
		m.renderModuleList(),
		m.renderFooter(),
	)
}

// selectionHint is the help line under the legend. The header box holds exactly
// one such line — a wrapped second line pushes the whole screen past the
// terminal height and bubbletea then drops the top row — so each tab offers
// progressively terser wordings and the first that fits wins.
func (m AppModel) selectionHint() string {
	avail := max(m.width-4, 1) // the box's inner width: border and padding take 4

	var variants []string
	switch {
	case m.filterMode:
		variants = []string{
			fmt.Sprintf("Filter: %s█ | ↑↓ move | Enter apply | Ctrl+U clear | Esc cancel", m.filterInput),
			fmt.Sprintf("Filter: %s█ | ↑↓ | Enter apply | Esc cancel", m.filterInput),
			fmt.Sprintf("Filter: %s█", m.filterInput),
		}
	case m.currentTab == tabUpdate:
		sel := fmt.Sprintf("Selected: %d/%d", m.countSelected(), len(m.updateModules))
		if m.verify {
			sel += " | build check on"
		}
		variants = []string{
			"↑↓ move | Space toggle | a all | s safe | v build check | Tab switch | / filter | Enter update | " + sel,
			"↑↓ move | Space toggle | a all | Tab switch | / filter | Enter update | " + sel,
			"↑↓ move | Space toggle | a all | / filter | Enter update | " + sel,
			"Space toggle | a all | / filter | Enter update | " + sel,
			"Space | a all | / filter | Enter update | " + sel,
			sel,
		}
	default:
		variants = []string{
			"↑↓ move | Space details | / filter | Tab switch | q quit  (read-only)",
			"↑↓ move | Space details | / filter | Tab switch  (read-only)",
			"Space details | / filter | Tab  (read-only)",
			"read-only",
		}
	}

	if m.filterInput != "" && !m.filterMode {
		prefix := fmt.Sprintf("filter '%s' (Esc clears) | ", m.filterInput)
		for i := range variants {
			variants[i] = prefix + variants[i]
		}
		// Falling back to the bare variants keeps the hint useful when the
		// prefix alone would crowd everything out.
		variants = append(variants, fmt.Sprintf("filter '%s' (Esc clears)", m.filterInput))
	}

	for _, v := range variants {
		if lipgloss.Width(v) <= avail {
			return v
		}
	}

	// Nothing fits. While typing, keep the tail of the input and the cursor in
	// view — clipping from the right would hide exactly what was just typed.
	if m.filterMode {
		runes := []rune(m.filterInput)
		room := max(avail-lipgloss.Width("Filter: …█"), 0)
		return "Filter: …" + string(runes[max(len(runes)-room, 0):]) + "█"
	}
	return lipgloss.NewStyle().MaxWidth(avail).Render(variants[len(variants)-1])
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

	helpText := m.helpStyle().
		Width(m.width - 4).
		Align(lipgloss.Center).
		Render(m.selectionHint())

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
// what each tab holds without visiting it. While a filter is applied the active
// tab shows matched/total.
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
		if m.currentTab == l.tab && m.filterInput != "" {
			text = fmt.Sprintf("%s (%d/%d)", l.name, m.visibleCount(), l.count)
		}
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
		if m.verifyRunning {
			status = fmt.Sprintf("%d/%d modules done — verifying the project still builds\n\n",
				m.currentModule, m.totalModules)
		}
		status += progressBar

		statusBox := lipgloss.NewStyle().
			Width(m.width - 4).
			Align(lipgloss.Center).
			Render(status)

		topSection.WriteString(statusBox)
		topSection.WriteString("\n")
	}

	// Ctrl+C is the only key honoured here, and it rolls back rather than
	// quits; say so, since the screen is otherwise inert.
	hint := "Ctrl+C: cancel and roll back go.mod / go.sum"
	if m.cancelling {
		hint = "Cancelling — restoring go.mod and go.sum… (Ctrl+C again to force quit)"
	}
	topSection.WriteString(m.helpStyle().Width(m.width - 4).Align(lipgloss.Center).Render(hint))

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

// renderUpdateLogs renders the update log pane, always pinned to the newest
// entry. Its geometry matches renderAnalyzingLogs; see logBoxHeight.
func (m AppModel) renderUpdateLogs() string {
	var b strings.Builder

	b.WriteString(m.st().logTitle.Render("Update Log"))
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
		if wasCancelled(m.updateError) {
			heading = "Update Cancelled — changes rolled back"
		}
		if errors.Is(m.updateError, updater.ErrVerifyFailed) {
			heading = "Build Check Failed — changes rolled back"
		}
		if !rolledBack(m.updateError) {
			heading = "Update Failed — ROLLBACK INCOMPLETE"
		}
		headStyle = m.st().failure
	}

	b.WriteString(headStyle.Width(m.width).Align(lipgloss.Center).Render(heading))
	b.WriteString("\n\n")

	// Read the applied set straight off the model. Deriving it by re-parsing
	// log strings used to yield an empty list, because the Updated flag was
	// only set after this screen had already rendered.
	updated := m.updatedThisRun()

	summary := fmt.Sprintf("Go version:       %s\n", m.targetGoVersion)
	if m.updateFailed {
		// The directive was rolled back with everything else; do not present
		// the requested version as if it had landed.
		summary = fmt.Sprintf("Go version:       unchanged (requested %s)\n", m.targetGoVersion)
	} else if m.actualGoVersion != "" && !m.goVersionMatched {
		// `go mod tidy` raises the directive when a remaining dependency needs
		// a newer toolchain; say so instead of echoing the request.
		summary = fmt.Sprintf("Go version:       %s  (requested %s)\n",
			m.actualGoVersion, m.targetGoVersion)
	}
	summary += fmt.Sprintf("Modules updated:  %d\n", len(updated))

	if len(updated) > 0 {
		summary += "\nUpdated modules:\n"
		// A long run must not grow the box past the terminal, or the heading
		// scrolls off the top.
		listed := updated
		if limit := max(3, m.height-16); len(listed) > limit {
			listed = listed[:limit]
		}
		for _, mod := range listed {
			summary += fmt.Sprintf("  • %s: %s → %s\n",
				mod.Info.Path, mod.Info.CurrentVersion, mod.Info.LatestCompatible)
		}
		if hidden := len(updated) - len(listed); hidden > 0 {
			summary += fmt.Sprintf("  … and %d more\n", hidden)
		}
	}

	if m.updateError != nil {
		switch {
		case !rolledBack(m.updateError):
			summary += "\nReason: " + m.reasonText() + "\n"
			summary += "go.mod / go.sum may be left half-updated; inspect them (git diff) before building."
		case wasCancelled(m.updateError):
			summary += "\nReason: cancelled by user\n"
			summary += "go.mod and go.sum were restored to their previous contents."
		default:
			summary += "\nReason: " + m.reasonText() + "\n"
			summary += "go.mod and go.sum were restored to their previous contents."
		}
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

// renderModuleList draws the visible window of the active tab, padded to the
// list height so the footer stays pinned.
//
// Only the rows on screen are rendered. The list used to be rendered whole and
// handed to a scrolling viewport on every key press, which cost time linear in
// the number of modules for a cursor move that changes two rows.
func (m AppModel) renderModuleList() string {
	h := m.getViewportHeight()

	indices := m.visibleIndices()
	if len(indices) == 0 {
		return padLines(m.emptyListMessage(), h)
	}

	top := max(0, min(m.listTop, len(indices)-h))
	end := min(len(indices), top+h)

	lines := make([]string, 0, h)
	for pos := top; pos < end; pos++ {
		lines = append(lines, m.renderRow(indices[pos], pos == m.cursor))
	}
	block := strings.Join(lines, "\n")

	// Safety net, not the layout: fitColumns already sizes rows to fit. A row
	// that is still too wide (a long replace directive, a tiny terminal) must
	// be cut here rather than wrap, because a wrapped row is two screen lines
	// and pushes the footer off the bottom.
	if m.width > 0 {
		block = lipgloss.NewStyle().MaxWidth(m.width).Render(block)
	}
	return padLines(block, h)
}

// renderRow draws entry i of the active tab.
func (m AppModel) renderRow(i int, isCursor bool) string {
	switch m.currentTab {
	case tabUpdate:
		return m.renderUpdateLine(m.updateModules[i], isCursor)
	case tabRequire:
		return m.renderRequireLine(m.requireModules[i], isCursor)
	case tabIndirect:
		return m.renderRequireLine(m.indirectModules[i], isCursor)
	case tabReplace:
		return m.renderReplaceLine(m.replaceModules[i], isCursor)
	}
	return ""
}

// emptyListMessage explains why the active tab shows nothing. A filter that
// matched nothing is the user's doing; an empty tab is a real result.
func (m AppModel) emptyListMessage() string {
	if m.filterInput != "" {
		return m.helpStyle().Render(fmt.Sprintf("  No modules match '%s'", m.filterInput))
	}

	switch m.currentTab {
	case tabUpdate:
		return m.helpStyle().Render(fmt.Sprintf(
			"  All direct dependencies are already at their newest Go %s-compatible version", m.targetGoVersion))
	case tabRequire:
		return m.helpStyle().Render("  No direct dependencies")
	case tabIndirect:
		return m.helpStyle().Render("  No indirect dependencies")
	case tabReplace:
		return m.helpStyle().Render("  No replace directives")
	}
	return ""
}

// padLines appends blank lines until s spans n lines.
func padLines(s string, n int) string {
	if missing := n - 1 - strings.Count(s, "\n"); missing > 0 {
		s += strings.Repeat("\n", missing)
	}
	return s
}

// pathColumnWidth is the most of a module path that is shown before eliding.
// Paths are truncated from the left so the distinguishing tail stays readable —
// "…/charmbracelet/bubbletea" tells you more than "github.com/charmbrace…".
const pathColumnWidth = 50

// minPathColumn is the narrowest the path column is squeezed to on a small
// terminal. Below this a path stops being recognisable; the end of the row (the
// "newer need a later Go" note) is clipped instead.
const minPathColumn = 24

// Width of everything in a row besides its path and version columns: the cursor
// mark, checkbox or indent, and the gaps between columns.
const (
	updateRowChrome  = 2 + 3 + 2 + 2
	requireRowChrome = 1 + 4 + 2
)

// pathColWidth is the path column width in effect.
func (m AppModel) pathColWidth() int {
	if m.pathCol > 0 {
		return m.pathCol
	}
	return pathColumnWidth
}

// minSqueezedColumn is the narrowest the path column is squeezed to before the
// row gives up part of its version column instead.
const minSqueezedColumn = 12

// fitColumns lays out the rows of the active tab for the terminal width.
//
// The version column is the point of an Update row, so the path column gives way
// before it does, and the optional parts go before either:
//
//  1. everything fits: path (at least 24 columns), "old → new", and the
//     "newer need a later Go" note;
//  2. no room for the note: drop it, keep the path;
//  3. still too tight: squeeze the path down to 12 columns;
//  4. a pair of pseudo-versions alone overflows: show only "→ new".
//
// The path column is as wide as the tab's longest path, capped at
// pathColumnWidth. Everything is measured over the whole tab, not the filtered
// view, so typing a filter does not make the columns jump.
func (m *AppModel) fitColumns() {
	m.hideNote, m.hideCurrent = false, false
	if m.width <= 0 || m.currentTab == tabReplace {
		return
	}

	longest := 0
	for i, n := 0, m.tabLen(); i < n; i++ {
		longest = max(longest, utf8.RuneCountInString(m.tabText(i)))
	}
	want := min(longest, pathColumnWidth)

	if m.currentTab != tabUpdate {
		reqs := m.requireModules
		if m.currentTab == tabIndirect {
			reqs = m.indirectModules
		}
		versions := 0
		for _, req := range reqs {
			versions = max(versions, utf8.RuneCountInString(req.Version))
		}
		m.pathCol = min(want, max(m.width-requireRowChrome-versions, minSqueezedColumn))
		return
	}

	both, newOnly, note := 0, 0, 0
	for _, mod := range m.updateModules {
		w := measureUpdate(mod.Info)
		both, newOnly, note = max(both, w.both), max(newOnly, w.newOnly), max(note, w.note)
	}

	avail := m.width - updateRowChrome
	switch {
	case avail-both-note >= min(want, minPathColumn):
		m.pathCol = min(want, avail-both-note)
	case avail-both >= min(want, minSqueezedColumn):
		m.hideNote = true
		m.pathCol = min(want, avail-both)
	default:
		m.hideNote, m.hideCurrent = true, true
		m.pathCol = max(min(want, avail-newOnly), 8)
	}
}

// updateWidths are the widths of the parts of an Update row's version column.
type updateWidths struct {
	both    int // "current → latest"
	newOnly int // "→ latest"
	note    int // "  (n newer need a later Go)", 0 when there is none
}

func measureUpdate(info *finder.ModuleInfo) updateWidths {
	latest := utf8.RuneCountInString(info.LatestCompatible)
	w := updateWidths{
		both:    utf8.RuneCountInString(info.CurrentVersion) + utf8.RuneCountInString(" → ") + latest,
		newOnly: utf8.RuneCountInString("→ ") + latest,
	}
	w.note = utf8.RuneCountInString(blockedNote(info)) + utf8.RuneCountInString(heldBackNote(info))
	return w
}

// blockedNote answers "why not the latest release?" for versions the target Go
// cannot build.
func blockedNote(info *finder.ModuleInfo) string {
	if info.BlockedNewer == 0 {
		return ""
	}
	return fmt.Sprintf("  (%d newer need a later Go)", info.BlockedNewer)
}

// heldBackNote answers it for versions kept back by --max-update.
func heldBackNote(info *finder.ModuleInfo) string {
	if info.HeldBack == 0 {
		return ""
	}
	return fmt.Sprintf("  (%d held back by --max-update)", info.HeldBack)
}

// elideTo shortens path to width columns by dropping its head, rune-aware.
func elideTo(path string, width int) string {
	runes := []rune(path)
	if len(runes) <= width {
		return path
	}
	if width <= 1 {
		return "…"
	}
	return "…" + string(runes[len(runes)-(width-1):])
}

// renderUpdateLine renders a line for Update tab (with checkbox and version changes)
func (m AppModel) renderUpdateLine(mod *SelectableModule, isCursor bool) string {
	modulePath := elideTo(mod.Info.Path, m.pathColWidth())

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
		latest := m.getVersionStyle(mod.Info.UpdateType).Render(mod.Info.LatestCompatible)
		if m.hideCurrent {
			versions = arrow + " " + latest
		} else {
			versions = fmt.Sprintf("%s %s %s", m.st().faint.Render(mod.Info.CurrentVersion), arrow, latest)
		}

		// Answer "why not the latest release?" on the row itself, instead of
		// leaving the count only in the log file.
		if !m.hideNote {
			versions += m.helpStyle().Render(blockedNote(mod.Info) + heldBackNote(mod.Info))
		}
	}

	line := fmt.Sprintf("%s  %-*s  %s", checkbox, m.pathColWidth(), modulePath, versions)
	if mod.Updated {
		line = m.st().faint.Render(line)
	}

	if isCursor {
		return m.cursorStyle().Render("▶ ") + line
	}
	return "  " + line
}

// renderRequireLine renders a read-only dependency row. Direct and indirect
// requires render identically, so they share one function.
func (m AppModel) renderRequireLine(req parser.Require, isCursor bool) string {
	line := fmt.Sprintf("    %-*s  %s", m.pathColWidth(), elideTo(req.Path, m.pathColWidth()), req.Version)

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

	b.WriteString(m.st().logTitle.Render("📝 Analysis Log"))
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
	return m.st().separator.Render(strings.Repeat("─", max(m.width-10, 1)))
}

// formatAnalysisLogLine renders a stored "[STATUS]pkg" entry with color.
func (m AppModel) formatAnalysisLogLine(logLine string) string {
	if strings.HasPrefix(logLine, "[") {
		if end := strings.IndexByte(logLine, ']'); end > 0 {
			if style, ok := m.st().analysis[strings.ToLower(logLine[1:end])]; ok {
				return "  " + style.text.Render(style.label) + " " + style.pkg.Render(logLine[end+1:])
			}
		}
	}

	return "  " + m.logStyle().Render(logLine)
}
