package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/elonnzhang/go-mod-select/pkg/analyzer"
	"github.com/elonnzhang/go-mod-select/pkg/finder"
	"github.com/elonnzhang/go-mod-select/pkg/logger"
	"github.com/elonnzhang/go-mod-select/pkg/parser"
	"github.com/elonnzhang/go-mod-select/pkg/scan"
	"github.com/elonnzhang/go-mod-select/pkg/updater"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Analysis status values shared between the worker and the renderer.
const (
	statusChecking = scan.StatusChecking
	statusFound    = scan.StatusFound
	statusSkipped  = scan.StatusSkipped
	statusError    = scan.StatusError
)

// logPrefix returns the tag used to store a status in analysisLogs. Storing
// the tag rather than pre-rendered text lets the renderer restyle on resize
// and lets a completion entry replace its own [CHECKING] placeholder.
func logPrefix(status string) string {
	return "[" + strings.ToUpper(status) + "]"
}

// updateOrAddAnalysisLog replaces the pending [CHECKING] entry for a package
// with its final status, or appends when there is nothing to replace.
func (m *AppModel) updateOrAddAnalysisLog(status, pkg string) {
	entry := logPrefix(status) + pkg

	if status == statusChecking {
		m.analysisLogs = append(m.analysisLogs, entry)
		return
	}

	checking := logPrefix(statusChecking) + pkg
	for i := len(m.analysisLogs) - 1; i >= 0; i-- {
		if m.analysisLogs[i] == checking {
			m.analysisLogs[i] = entry
			return
		}
	}

	m.analysisLogs = append(m.analysisLogs, entry)
}

// addOperationLog adds a log entry for the selection screen (keep last 3 only)
func (m *AppModel) addOperationLog(msg string) {
	m.operationLogs = append(m.operationLogs, msg)
	if len(m.operationLogs) > 3 {
		m.operationLogs = m.operationLogs[len(m.operationLogs)-3:]
	}
}

// detailLoadingNotice is appended to a popup's header until the asynchronous
// body arrives, then replaced by it.
const detailLoadingNotice = "\n  (loading…)\n"

// inRange reports whether the cursor addresses a real element of a list of
// length n. The lower bound matters: an empty tab yields a cursor of -1, and
// `-1 < 0` passes a length-only check while indexing out of range.
func inRange(cursor, n int) bool {
	return cursor >= 0 && cursor < n
}

// detailSubject identifies what the currently open popup describes, so a slow
// result arriving after the user moved on can be discarded.
func (m AppModel) detailSubject() string {
	i, ok := m.selectedIndex()
	if !ok {
		return ""
	}
	switch m.currentTab {
	case tabRequire:
		return m.requireModules[i].Path
	case tabIndirect:
		return m.indirectModules[i].Path
	case tabReplace:
		return m.replaceModules[i].Old
	}
	return ""
}

// detailHeader is the part of a detail popup that needs no subprocess, so it
// can render instantly while the slow part loads.
func (m AppModel) detailHeader() (title string, ok bool) {
	i, ok := m.selectedIndex()
	if !ok {
		return "", false
	}

	switch m.currentTab {
	case tabRequire:
		req := m.requireModules[i]
		return fmt.Sprintf("📦 Require Directive Details\n\nModule Path:  %s\nVersion:      %s\nType:         Direct dependency\n",
			req.Path, req.Version), true

	case tabIndirect:
		req := m.indirectModules[i]
		return fmt.Sprintf("📦 Indirect Require Details\n\nModule Path:  %s\nVersion:      %s\nType:         Indirect dependency\n",
			req.Path, req.Version), true

	case tabReplace:
		rep := m.replaceModules[i]
		return fmt.Sprintf("🔄 Replace Directive Details\n\nOriginal:     %s\nReplacement:  %s\n\n"+
			"Replace directives override a dependency with a local or forked copy.\n",
			rep.Old, rep.New), true
	}

	return "", false
}

// loadDetailBody runs the slow part of a detail popup off the event loop.
//
// `go mod why` and a full source walk each take long enough to be felt as a
// freeze; running them inline in Update stalled every keystroke and the spinner
// until they returned.
func (m AppModel) loadDetailBody() tea.Cmd {
	i, ok := m.selectedIndex()
	if !ok {
		return nil
	}

	tab := m.currentTab
	root := m.projectRoot
	seq := m.detailSeq

	var modPath string
	switch tab {
	case tabRequire:
		modPath = m.requireModules[i].Path
	case tabIndirect:
		modPath = m.indirectModules[i].Path
	default:
		return nil
	}

	if modPath == "" {
		return nil
	}

	return func() tea.Msg {
		var b strings.Builder

		switch tab {
		case tabRequire:
			b.WriteString("\n🔍 Used in files:\n")
			files, err := analyzer.FindFilesUsingPackage(root, modPath)
			switch {
			case err != nil:
				b.WriteString(fmt.Sprintf("  (Error analyzing: %v)\n", err))
			case len(files) == 0:
				b.WriteString("  (No direct imports found — may be used only by tests or tooling)\n")
			default:
				for _, file := range files {
					b.WriteString("  • " + file + "\n")
				}
			}

		case tabIndirect:
			b.WriteString("\n🔗 Required by:\n")
			for _, reason := range analyzer.FindWhyPackageNeeded(root, modPath) {
				b.WriteString(reason + "\n")
			}
			b.WriteString("\n💡 Indirect dependencies are modules your dependencies need, " +
				"which your own code does not import directly.\n")
		}

		return detailLoadedMsg{module: modPath, body: b.String(), seq: seq}
	}
}

// setDetailContent loads the popup text into its viewport, wrapped to the
// viewport's width. The viewport clips rather than wraps, so an unwrapped long
// line (a deep file path) would silently lose its tail.
func (m *AppModel) setDetailContent() {
	w, _ := m.detailViewportSize()
	m.detailViewport.SetContent(lipgloss.NewStyle().Width(w).Render(m.detailContent))
}

// markUpdated flags a module as applied so the list and completion summary can
// distinguish it from one still pending.
func (m *AppModel) markUpdated(path string) {
	for _, mod := range m.updateModules {
		if mod.Info != nil && mod.Info.Path == path {
			mod.Updated = true
			return
		}
	}
}

// updatedPaths is the set of modules applied so far.
func (m AppModel) updatedPaths() map[string]bool {
	set := make(map[string]bool)
	for _, mod := range m.updatedModules() {
		set[mod.Info.Path] = true
	}
	return set
}

// updatedThisRun returns the modules the latest run applied: those updated now
// that were not already updated when it started.
func (m AppModel) updatedThisRun() []*SelectableModule {
	var out []*SelectableModule
	for _, mod := range m.updatedModules() {
		if !m.updatedBefore[mod.Info.Path] {
			out = append(out, mod)
		}
	}
	return out
}

// updatedModules returns the modules applied so far, in list order.
func (m AppModel) updatedModules() []*SelectableModule {
	var out []*SelectableModule
	for _, mod := range m.updateModules {
		if mod.Updated {
			out = append(out, mod)
		}
	}
	return out
}

// maxAnalyzingLogOffset is the largest scroll offset that still shows content.
func (m AppModel) maxAnalyzingLogOffset() int {
	return max(0, len(m.analysisLogs)-m.getAnalyzingLogVisibleLines())
}

// Fixed chrome heights used to size the scrollable module list. These match
// the rows emitted by renderSelectionHeader/renderTabs/renderFooter.
const (
	headerHeight = 8 // title + legend + help inside a bordered box, plus tabs
	footerHeight = 6 // separator + "Activity Log" title + 3 log lines + slack
	minViewport  = 3
)

// getViewportHeight is the number of list rows on screen, clamped so a short
// terminal still renders a usable list instead of a negative height.
func (m AppModel) getViewportHeight() int {
	return max(minViewport, m.height-headerHeight-footerHeight)
}

// pageSize is how far PgUp/PgDn move: a screenful, keeping one row of overlap
// so the eye keeps its place.
func (m AppModel) pageSize() int {
	return max(1, m.getViewportHeight()-1)
}

// tabLen is the number of entries in the active tab before filtering.
func (m AppModel) tabLen() int {
	switch m.currentTab {
	case tabUpdate:
		return len(m.updateModules)
	case tabRequire:
		return len(m.requireModules)
	case tabIndirect:
		return len(m.indirectModules)
	case tabReplace:
		return len(m.replaceModules)
	}
	return 0
}

// tabText is the text the filter matches for entry i of the active tab.
func (m AppModel) tabText(i int) string {
	switch m.currentTab {
	case tabUpdate:
		return m.updateModules[i].Info.Path
	case tabRequire:
		return m.requireModules[i].Path
	case tabIndirect:
		return m.indirectModules[i].Path
	case tabReplace:
		return m.replaceModules[i].Old + " " + m.replaceModules[i].New
	}
	return ""
}

// visibleIndices returns indices into the active tab's entries that match the
// filter (case-insensitively), in list order. The filter applies to every tab.
func (m AppModel) visibleIndices() []int {
	n := m.tabLen()
	indices := make([]int, 0, n)

	if m.filterInput == "" {
		for i := 0; i < n; i++ {
			indices = append(indices, i)
		}
		return indices
	}

	filter := strings.ToLower(m.filterInput)
	for i := 0; i < n; i++ {
		if strings.Contains(strings.ToLower(m.tabText(i)), filter) {
			indices = append(indices, i)
		}
	}
	return indices
}

// visibleCount is len(visibleIndices()) without building the slice when no
// filter is active, which is every cursor move.
func (m AppModel) visibleCount() int {
	if m.filterInput == "" {
		return m.tabLen()
	}
	return len(m.visibleIndices())
}

// getMaxCursor is the last valid cursor position; -1 when the list is empty.
func (m AppModel) getMaxCursor() int {
	return m.visibleCount() - 1
}

// selectedIndex maps the cursor to an index into the active tab's entries.
func (m AppModel) selectedIndex() (int, bool) {
	indices := m.visibleIndices()
	if !inRange(m.cursor, len(indices)) {
		return 0, false
	}
	return indices[m.cursor], true
}

// moveCursor puts the cursor at pos, clamped to the list, and scrolls to keep
// it on screen. The clamp to 0 matters on an empty list, where the last valid
// position is -1: a cursor of -1 would stick and index out of range.
func (m *AppModel) moveCursor(pos int) {
	m.cursor = max(0, min(pos, m.getMaxCursor()))
	m.scrollToCursor()
}

// scrollToCursor shifts the visible window the minimum distance that includes
// the cursor row.
func (m *AppModel) scrollToCursor() {
	h := m.getViewportHeight()
	if m.cursor < m.listTop {
		m.listTop = m.cursor
	} else if m.cursor >= m.listTop+h {
		m.listTop = m.cursor - h + 1
	}
	m.listTop = max(0, m.listTop)
}

// clampList re-establishes the cursor and window invariants after the list or
// the terminal height changed underneath them.
func (m *AppModel) clampList() {
	m.cursor = max(0, min(m.cursor, m.getMaxCursor()))
	m.listTop = max(0, min(m.listTop, m.visibleCount()-m.getViewportHeight()))
	m.scrollToCursor()
}

// resetList returns to the top of the list after the filter changed.
func (m *AppModel) resetList() {
	m.cursor = 0
	m.listTop = 0
}

// switchTab activates a tab, starting at its top.
func (m *AppModel) switchTab(tab tabType) {
	m.currentTab = tab
	m.resetList()
	m.fitColumns()
}

func (m AppModel) countSelected() int {
	count := 0
	for _, mod := range m.updateModules {
		if mod.Selected {
			count++
		}
	}
	return count
}

// replaceInfos renders go.mod's replace directives for display.
func replaceInfos(goMod *parser.GoMod) []ReplaceInfo {
	var replaces []ReplaceInfo
	for _, rep := range goMod.Replace {
		oldModule := rep.OldPath
		if rep.OldVersion != "" {
			oldModule += "@" + rep.OldVersion
		}
		newModule := rep.NewPath
		if rep.NewVersion != "" {
			newModule += "@" + rep.NewVersion
		}
		replaces = append(replaces, ReplaceInfo{Old: oldModule, New: newModule})
	}
	return replaces
}

// analyzeModules starts the analysis process and returns a subscription command
func (m AppModel) analyzeModules() tea.Cmd {
	return func() tea.Msg {
		directRequires, indirectRequires := parser.SplitRequires(m.goMod.Requires)

		total := len(directRequires)
		if total == 0 {
			return analyzeCompleteMsg{
				requireModules:    []*finder.ModuleInfo{},
				indirectModules:   indirectRequires,
				replaces:          []ReplaceInfo{},
				completedPackages: []scan.Result{},
			}
		}

		// Start the analysis subscription
		return startAnalysisMsg{
			directRequires:   directRequires,
			indirectRequires: indirectRequires,
			targetGoVersion:  m.targetGoVersion,
			goMod:            m.goMod,
			projectRoot:      m.projectRoot,
			policy:           m.policy,
		}
	}
}

// startAnalysisMsg is the message to begin analysis
type startAnalysisMsg struct {
	directRequires   []parser.Require
	indirectRequires []parser.Require
	targetGoVersion  string
	goMod            *parser.GoMod
	// projectRoot is the analysed module's directory. The finder normally runs
	// its queries in a sandbox module, but falls back to this when no sandbox
	// can be created — and either way it must never be the tool's own cwd.
	projectRoot string
	policy      finder.Policy
}

// waitForAnalysisMsg creates a command that waits for the next message from the subscription
func waitForAnalysisMsg(sub <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-sub
	}
}

// send delivers a message unless the program is shutting down.
//
// The channel is unbuffered and its only reader is a bubbletea Cmd. Once the
// program quits, nothing reads again — a plain `sub <- msg` would block that
// worker forever, so wg.Wait() would never return and the Finder's scratch
// directory would never be removed.
func send(sub chan<- tea.Msg, done <-chan struct{}, msg tea.Msg) bool {
	select {
	case sub <- msg:
		return true
	case <-done:
		return false
	}
}

// startAnalysisWorker performs the analysis and sends progress updates through
// the channel. It returns as soon as done is closed.
func startAnalysisWorker(msg startAnalysisMsg, sub chan<- tea.Msg, done <-chan struct{}) {
	// A panic here would otherwise kill the process outside bubbletea's
	// recover, leaving the terminal in raw mode with the alt screen entered.
	defer func() {
		if r := recover(); r != nil {
			logger.Log.Error("analysis worker panicked", "panic", fmt.Sprint(r))
			send(sub, done, analyzeCompleteMsg{
				requireModules:  []*finder.ModuleInfo{},
				indirectModules: msg.indirectRequires,
			})
		}
	}()

	// Cancelling the finder's context kills the in-flight `go list` children
	// instead of orphaning a dozen proxy requests.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-done:
			cancel()
		case <-ctx.Done():
		}
	}()

	f := finder.NewWithContext(ctx, msg.projectRoot)
	f.Policy = msg.policy
	defer f.Close()

	lookup := func(path, current string) (*finder.ModuleInfo, error) {
		return f.FindCompatibleVersions(path, current, msg.targetGoVersion)
	}
	// send blocks until the UI reads or the program quits; scan treats a false
	// return as "abandon this lookup".
	results := scan.Run(ctx, lookup, msg.directRequires, func(ev scan.Event) bool {
		return send(sub, done, analyzeProgressMsg{
			packageName: ev.Pkg,
			current:     ev.Completed,
			total:       ev.Total,
			status:      ev.Status,
		})
	})

	send(sub, done, analyzeCompleteMsg{
		requireModules:    scan.Updatable(results),
		indirectModules:   msg.indirectRequires,
		replaces:          replaceInfos(msg.goMod),
		completedPackages: results,
	})
}

func (m AppModel) handleAnalyzeComplete(msg analyzeCompleteMsg) (tea.Model, tea.Cmd) {
	// Update analyzing count to show completion
	m.analyzingCount = m.analyzingTotal
	m.analyzingPackage = ""

	// Store modules with available updates in updateModules (can select)
	m.updateModules = make([]*SelectableModule, len(msg.requireModules))
	for i, mod := range msg.requireModules {
		m.updateModules[i] = &SelectableModule{
			Info:     mod,
			Selected: false,
		}
	}

	// Direct and indirect requires come from go.mod for the read-only tabs.
	m.requireModules, m.indirectModules = parser.SplitRequires(m.goMod.Requires)
	m.replaceModules = msg.replaces

	// The terminal size is already known, so lay the list out right here.
	// (This used to bounce a synthetic WindowSizeMsg through the event loop,
	// carrying a size that a real resize could overtake.)
	m.currentScreen = screenSelection
	m.resetList()
	m.fitColumns()

	return m, nil
}

// performUpdate executes the actual update with real-time progress
func (m AppModel) performUpdate() tea.Cmd {
	return func() tea.Msg {
		selected := []*finder.ModuleInfo{}
		for _, mod := range m.updateModules {
			// An already-applied module must not be re-fetched on a second
			// round through the selection screen.
			if mod.Selected && !mod.Updated {
				selected = append(selected, mod.Info)
			}
		}

		// Start the update subscription
		return startUpdateMsg{
			modules:         selected,
			targetGoVersion: m.targetGoVersion,
			goMod:           m.goMod,
			verify:          m.verify,
		}
	}
}

// waitForUpdateMsg creates a command that waits for the next update message
func waitForUpdateMsg(sub <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-sub
	}
}

// startUpdateWorker performs the update and sends progress through the channel.
//
// Unlike the analysis worker this one does not abort when the UI goes away: it
// is midway through rewriting go.mod, and stopping between `go get` calls would
// leave the module in a state neither the user nor the rollback expects. It
// stops only through ctx, which makes the updater interrupt the go command in
// flight and roll both files back. done merely turns shutdown into such a
// cancellation, and stops sends from blocking once the UI has gone away.
func startUpdateWorker(ctx context.Context, cancel context.CancelFunc, msg startUpdateMsg, sub chan<- tea.Msg, done <-chan struct{}) {
	defer cancel()
	go func() {
		select {
		case <-done:
			cancel()
		case <-ctx.Done():
		}
	}()

	var upd *updater.Updater
	committed := false // the run finished; there is nothing left to undo
	defer func() {
		if r := recover(); r != nil {
			logger.Log.Error("update worker panicked", "panic", fmt.Sprint(r), "committed", committed)
			if committed {
				// The update itself succeeded and stays on disk; only the
				// follow-up reads failed. Reporting a failure here would claim a
				// rollback that never happened.
				send(sub, done, updateCompleteMsg{goVersionMatched: true})
				return
			}
			err := fmt.Errorf("internal error: %v", r)
			// The panic may have struck between two go commands, with go.mod
			// already rewritten; undo that before reporting.
			if upd != nil && !committed {
				if rbErr := upd.Rollback(); rbErr != nil {
					err = fmt.Errorf("%w (%w)", err, rbErr)
				}
			}
			send(sub, done, updateFailedMsg{err: err})
			send(sub, done, updateCompleteMsg{})
		}
	}()

	upd = updater.NewUpdater(msg.goMod).WithContext(ctx)
	if msg.verify {
		upd.WithVerify()
	}

	progressCb := func(current, total int, module, status string) {
		send(sub, done, updateProgressMsg{
			current: current,
			total:   total,
			module:  module,
			status:  status,
		})
	}

	if err := upd.UpdateAllWithProgress(msg.targetGoVersion, msg.modules, false, progressCb); err != nil {
		// The updater has already rolled both files back; surface the reason
		// as a final log line and let the model flip to the failure summary.
		send(sub, done, updateFailedMsg{err: err})
		send(sub, done, updateCompleteMsg{})
		return
	}

	committed = true

	// `go mod tidy` can raise the go directive above what was requested when a
	// remaining dependency needs a newer toolchain, so report what is actually
	// on disk instead of echoing the request.
	actual, matched, err := upd.VerifyGoVersion(msg.targetGoVersion)

	// Tidy may also have added, dropped or re-pinned requirements; hand back the
	// file as it is now so the read-only tabs do not show startup's version.
	refreshed, perr := parser.ParseGoMod(msg.goMod.FilePath)
	if perr != nil {
		logger.Log.Warn("could not re-read go.mod after update", "error", perr.Error())
		refreshed = nil
	}

	send(sub, done, updateCompleteMsg{
		actualGoVersion:  actual,
		goVersionMatched: matched || err != nil,
		refreshed:        refreshed,
	})
}

// logBoxHeight is the fixed height of the bottom log pane on the analyzing and
// updating screens. It must be exactly half the screen so the two panes joined
// vertically total the terminal height — one line more and bubbletea's renderer
// drops the topmost line, cutting the banner.
func (m AppModel) logBoxHeight() int {
	h := m.height / 2
	if h < 10 {
		h = 10
	}
	return h
}

// getAnalyzingLogVisibleLines is how many log rows fit in the pane.
//
// Budget for a pane of height H: 1 line of top padding + title + separator +
// rows + status line + 1 line of bottom padding. So rows = H - 5.
func (m AppModel) getAnalyzingLogVisibleLines() int {
	rows := m.logBoxHeight() - 5
	if rows < 3 {
		rows = 3
	}
	return rows
}

// errorLines splits an error into at most maxLines display rows of at most
// width runes. go failures embed their entire stderr, which can run to dozens of
// lines; shown raw it outgrows the terminal and bubbletea drops the top rows,
// heading included. The full text stays in the log file.
func errorLines(err error, maxLines, width int) []string {
	var rows []string
	for _, line := range strings.Split(err.Error(), "\n") {
		line = strings.TrimRight(line, " \t\r")
		if line == "" {
			continue
		}
		if r := []rune(line); len(r) > width {
			line = string(r[:max(width-1, 0)]) + "…"
		}
		rows = append(rows, line)
	}
	if len(rows) > maxLines {
		hidden := len(rows) - maxLines
		rows = append(rows[:maxLines], fmt.Sprintf("… %d more line(s); full output in ~/.go-mod-select/app.log", hidden))
	}
	return rows
}

// reasonText is the failure reason for the summary box, clipped to fit it and
// with continuation rows aligned under the first.
func (m AppModel) reasonText() string {
	return strings.Join(errorLines(m.updateError, 8, max(m.width-16, 20)), "\n        ")
}

// handleVerifyProgress logs a step of the post-update build check.
func (m *AppModel) handleVerifyProgress(status string) {
	var entry string
	switch status {
	case "checking":
		entry = "⏳ Checking that the project builds before the update…"
	case "skipped":
		entry = "- Build check skipped: the project does not build as it stands"
	case "verifying":
		entry = "⏳ Verifying `go build ./...`…"
		m.verifyRunning = true
	case "success":
		entry = "✓ The project still builds"
		m.verifyRunning = false
	case "error":
		entry = "✗ The project no longer builds after the update"
		m.verifyRunning = false
		m.updateFailed = true
	}
	if entry != "" {
		m.updateLogs = append(m.updateLogs, entry)
	}
}

// wasCancelled reports whether an update run ended because the user cancelled.
func wasCancelled(err error) bool { return errors.Is(err, updater.ErrCancelled) }

// rolledBack reports whether a failed run left go.mod and go.sum restored.
func rolledBack(err error) bool { return !errors.Is(err, updater.ErrRollbackFailed) }

// shutdownGrace bounds how long TUI waits for workers to clean up after the
// program quits: killing `go list` children, removing the scratch module, or
// rolling back an interrupted update.
const shutdownGrace = 10 * time.Second

// waitWorkers waits for tracked workers, up to grace.
func waitWorkers(wg *sync.WaitGroup, grace time.Duration) {
	if wg == nil {
		return
	}
	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()
	// The terminal is already restored by now; say why exit is not instant.
	notice := time.AfterFunc(300*time.Millisecond, func() {
		fmt.Fprintln(os.Stderr, "go-mod-select: finishing cleanup (restoring go.mod/go.sum, removing temp files)…")
	})
	defer notice.Stop()

	select {
	case <-finished:
	case <-time.After(grace):
		logger.Log.Warn("workers still running at exit", "grace", grace.String())
	}
}

// TUI runs the interactive application. It applies the updates itself (via the
// updating screen), so it reports only whether the session ran cleanly.
//
// It deliberately returns no module list: it used to hand back the selected
// modules, and the caller applied them a second time on top of the update the
// TUI had already performed.
func TUI(targetGoVersion string, goMod *parser.GoMod, projectRoot string, opts Options) error {
	m := InitialAppModel(targetGoVersion, goMod, projectRoot, opts)

	p := tea.NewProgram(m, tea.WithAltScreen())

	// bubbletea traps SIGINT and SIGTERM but not SIGHUP, so closing the terminal
	// (or dropping an ssh session) mid-update killed the process with go.mod
	// half-written. Treat it as a quit: that cancels and rolls back like Ctrl+C.
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go func() {
		select {
		case <-hup:
			p.Quit()
		case <-m.quit:
		}
	}()

	// Signal shutdown once Run returns so any worker still in flight stops
	// instead of blocking forever on a channel the event loop no longer reads,
	// then wait for it to finish cleaning up. Returning at once let the process
	// exit before a worker could remove the finder's scratch directory or the
	// updater could roll back, leaking the former and abandoning the latter.
	defer func() {
		// Past this point the terminal is in cooked mode again, so a further
		// Ctrl+C is a real SIGINT that would kill us mid-rollback. The shield is
		// installed before the workers are released, and before the SIGHUP
		// handler goes away, so no signal arrives to a default action.
		shield := make(chan os.Signal, 8)
		signal.Notify(shield, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		defer signal.Stop(shield)
		signal.Stop(hup)

		close(m.quit)
		waitWorkers(m.workers, shutdownGrace)
	}()

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("error running TUI: %w", err)
	}
	return nil
}
