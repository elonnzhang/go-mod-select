package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"go-mod-select/pkg/analyzer"
	"go-mod-select/pkg/finder"
	"go-mod-select/pkg/logger"
	"go-mod-select/pkg/parser"
	"go-mod-select/pkg/updater"

	tea "github.com/charmbracelet/bubbletea"
)

// Analysis status values shared between the worker and the renderer.
const (
	statusChecking = "checking"
	statusFound    = "found"
	statusSkipped  = "skipped"
	statusError    = "error"
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
	switch m.currentTab {
	case tabRequire:
		if inRange(m.cursor, len(m.requireModules)) {
			return m.requireModules[m.cursor].Path
		}
	case tabIndirect:
		if inRange(m.cursor, len(m.indirectModules)) {
			return m.indirectModules[m.cursor].Path
		}
	case tabReplace:
		if inRange(m.cursor, len(m.replaceModules)) {
			return m.replaceModules[m.cursor].Old
		}
	}
	return ""
}

// detailHeader is the part of a detail popup that needs no subprocess, so it
// can render instantly while the slow part loads.
func (m AppModel) detailHeader() (title string, ok bool) {
	switch m.currentTab {
	case tabRequire:
		if !inRange(m.cursor, len(m.requireModules)) {
			return "", false
		}
		req := m.requireModules[m.cursor]
		return fmt.Sprintf("📦 Require Directive Details\n\nModule Path:  %s\nVersion:      %s\nType:         Direct dependency\n",
			req.Path, req.Version), true

	case tabIndirect:
		if !inRange(m.cursor, len(m.indirectModules)) {
			return "", false
		}
		req := m.indirectModules[m.cursor]
		return fmt.Sprintf("📦 Indirect Require Details\n\nModule Path:  %s\nVersion:      %s\nType:         Indirect dependency\n",
			req.Path, req.Version), true

	case tabReplace:
		if !inRange(m.cursor, len(m.replaceModules)) {
			return "", false
		}
		rep := m.replaceModules[m.cursor]
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
	tab := m.currentTab
	root := m.projectRoot

	var modPath string
	switch tab {
	case tabRequire:
		if inRange(m.cursor, len(m.requireModules)) {
			modPath = m.requireModules[m.cursor].Path
		}
	case tabIndirect:
		if inRange(m.cursor, len(m.indirectModules)) {
			modPath = m.indirectModules[m.cursor].Path
		}
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

		return detailLoadedMsg{module: modPath, body: b.String()}
	}
}

func (m *AppModel) updateViewport() {
	if !m.ready {
		return
	}
	content := m.renderModuleList()
	m.viewport.SetContent(content)
	// Content shrinking (e.g. a narrower filter) can leave YOffset past the
	// end, which renders as a blank pane.
	if m.viewport.YOffset > m.viewport.TotalLineCount()-m.viewport.Height {
		m.viewport.GotoBottom()
	}
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

func (m *AppModel) syncViewportToCursor() {
	if !m.ready {
		return
	}

	// One list row per line, so the cursor index is its line number.
	cursorLine := m.cursor
	top := m.viewport.YOffset
	bottom := top + m.viewport.Height - 1

	if cursorLine > bottom {
		m.viewport.ScrollDown(cursorLine - bottom)
	} else if cursorLine < top {
		m.viewport.ScrollUp(top - cursorLine)
	}
}

// Fixed chrome heights used to size the scrollable module list. These match
// the rows emitted by renderSelectionHeader/renderTabs/renderFooter.
const (
	headerHeight = 8 // title + legend + help inside a bordered box, plus tabs
	footerHeight = 6 // separator + "Activity Log" title + 3 log lines + slack
	minViewport  = 3
)

func (m AppModel) getHeaderHeight() int { return headerHeight }

func (m AppModel) getFooterHeight() int { return footerHeight }

// getViewportHeight is clamped so a short terminal still renders a usable
// list instead of a negative-height viewport.
func (m AppModel) getViewportHeight() int {
	return max(minViewport, m.height-headerHeight-footerHeight)
}

func (m AppModel) getMaxCursor() int {
	switch m.currentTab {
	case tabUpdate:
		return len(m.getVisibleUpdateModules()) - 1
	case tabRequire:
		return len(m.requireModules) - 1
	case tabIndirect:
		return len(m.indirectModules) - 1
	case tabReplace:
		return len(m.replaceModules) - 1
	}
	return 0
}

// getVisibleUpdateModules returns indices into updateModules that match the
// active filter, matching case-insensitively on the module path.
func (m AppModel) getVisibleUpdateModules() []int {
	if m.filterInput == "" {
		indices := make([]int, len(m.updateModules))
		for i := range m.updateModules {
			indices[i] = i
		}
		return indices
	}

	var visible []int
	filter := strings.ToLower(m.filterInput)
	for i, mod := range m.updateModules {
		if strings.Contains(strings.ToLower(mod.Info.Path), filter) {
			visible = append(visible, i)
		}
	}
	return visible
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

// analyzeModules starts the analysis process and returns a subscription command
func (m AppModel) analyzeModules() tea.Cmd {
	return func() tea.Msg {
		// Separate direct and indirect requires
		var directRequires []parser.Require
		var indirectRequires []parser.Require
		for _, req := range m.goMod.Requires {
			if req.Indirect {
				indirectRequires = append(indirectRequires, req)
			} else {
				directRequires = append(directRequires, req)
			}
		}

		total := len(directRequires)
		if total == 0 {
			return analyzeCompleteMsg{
				requireModules:    []*finder.ModuleInfo{},
				indirectModules:   indirectRequires,
				replaces:          []ReplaceInfo{},
				completedPackages: []AnalyzedPackage{},
			}
		}

		// Start the analysis subscription
		return startAnalysisMsg{
			directRequires:   directRequires,
			indirectRequires: indirectRequires,
			targetGoVersion:  m.targetGoVersion,
			goMod:            m.goMod,
			projectRoot:      m.projectRoot,
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
}

// waitForAnalysisMsg creates a command that waits for the next message from the subscription
func waitForAnalysisMsg(sub <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-sub
	}
}

// analyzeConcurrency bounds how many `go list` subprocesses run at once.
//
// Each worker spawns a subprocess that does network I/O against the module
// proxy. The previous limit of 200 spawned hundreds of processes for a large
// go.mod, which the proxy rate-limits and the OS charges for in context
// switches; a modest pool is both faster in practice and far kinder to the
// machine.
const analyzeConcurrency = 12

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

	total := len(msg.directRequires)

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, analyzeConcurrency)

	// Results are collected under a mutex rather than through a channel so the
	// worker never blocks on a reader that has gone away.
	var mu sync.Mutex
	completedPackages := make([]AnalyzedPackage, 0, total)

	var completed int32

	// Cancelling the finder's context kills the in-flight `go list` children
	// instead of orphaning a dozen proxy requests.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-done
		cancel()
	}()

	f := finder.NewWithContext(ctx, msg.projectRoot)
	defer f.Close()

	for _, req := range msg.directRequires {
		wg.Add(1)
		go func(r parser.Require) {
			defer wg.Done()

			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-done:
				return
			}

			if !send(sub, done, analyzeProgressMsg{
				packageName: r.Path,
				current:     int(atomic.LoadInt32(&completed)),
				total:       total,
				status:      statusChecking,
			}) {
				return
			}

			info, err := f.FindCompatibleVersions(r.Path, r.Version, msg.targetGoVersion)
			pkg := AnalyzedPackage{
				Pkg:    r.Path,
				Info:   info,
				Err:    err,
				Status: classify(info, err, r.Version),
			}

			current := int(atomic.AddInt32(&completed, 1))

			if !send(sub, done, analyzeProgressMsg{
				packageName: r.Path,
				current:     current,
				total:       total,
				status:      pkg.Status,
			}) {
				return
			}

			mu.Lock()
			completedPackages = append(completedPackages, pkg)
			mu.Unlock()
		}(req)
	}

	wg.Wait()

	// Stable order so the Update tab does not shuffle between runs.
	sort.Slice(completedPackages, func(i, j int) bool {
		return completedPackages[i].Pkg < completedPackages[j].Pkg
	})

	requireModules := extractUpdatable(completedPackages)

	var replaces []ReplaceInfo
	for _, rep := range msg.goMod.Replace {
		oldModule := rep.OldPath
		if rep.OldVersion != "" {
			oldModule += "@" + rep.OldVersion
		}
		newModule := rep.NewPath
		if rep.NewVersion != "" {
			newModule += "@" + rep.NewVersion
		}
		replaces = append(replaces, ReplaceInfo{
			Old: oldModule,
			New: newModule,
		})
	}

	send(sub, done, analyzeCompleteMsg{
		requireModules:    requireModules,
		indirectModules:   msg.indirectRequires,
		replaces:          replaces,
		completedPackages: completedPackages,
	})
}

// classify maps an analysis result to the status string the TUI renders.
func classify(info *finder.ModuleInfo, err error, currentVersion string) string {
	switch {
	case err != nil:
		return statusError
	case info != nil && info.LatestCompatible != currentVersion:
		return statusFound
	default:
		return statusSkipped
	}
}

// extractUpdatable keeps only the modules whose newest compatible version
// differs from the version currently pinned in go.mod.
func extractUpdatable(pkgs []AnalyzedPackage) []*finder.ModuleInfo {
	var out []*finder.ModuleInfo
	for _, pkg := range pkgs {
		if pkg.Err == nil && pkg.Info != nil && pkg.Info.LatestCompatible != pkg.Info.CurrentVersion {
			out = append(out, pkg.Info)
		}
	}
	return out
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

	// Separate direct and indirect requires from go.mod for read-only display
	var directRequires []parser.Require
	var indirectRequires []parser.Require
	for _, req := range m.goMod.Requires {
		if req.Indirect {
			indirectRequires = append(indirectRequires, req)
		} else {
			directRequires = append(directRequires, req)
		}
	}

	m.requireModules = directRequires
	m.indirectModules = indirectRequires
	m.replaceModules = msg.replaces

	// Switch to selection screen
	m.currentScreen = screenSelection
	m.ready = false // Will be set to true on next WindowSizeMsg

	return m, func() tea.Msg {
		return tea.WindowSizeMsg{Width: m.width, Height: m.height}
	}
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
// Unlike the analysis worker this one does not abort on `done`: it is midway
// through rewriting go.mod, and stopping between `go get` calls would leave the
// module in a state neither the user nor the rollback expects. It only uses
// done to avoid blocking on sends once the UI has gone away.
func startUpdateWorker(msg startUpdateMsg, sub chan<- tea.Msg, done <-chan struct{}) {
	defer func() {
		if r := recover(); r != nil {
			logger.Log.Error("update worker panicked", "panic", fmt.Sprint(r))
			send(sub, done, updateFailedMsg{err: fmt.Errorf("internal error: %v", r)})
			send(sub, done, updateCompleteMsg{})
		}
	}()

	upd := updater.NewUpdater(msg.goMod)

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

	// `go mod tidy` can raise the go directive above what was requested when a
	// remaining dependency needs a newer toolchain, so report what is actually
	// on disk instead of echoing the request.
	actual, matched, err := upd.VerifyGoVersion(msg.targetGoVersion)
	send(sub, done, updateCompleteMsg{actualGoVersion: actual, goVersionMatched: matched || err != nil})
}

// analyzePackagesParallel analyzes packages in parallel and returns detailed
// results, sorted by module path. Shared by the TUI-free CLI modes.
func analyzePackagesParallel(requires []parser.Require, targetGoVersion, projectRoot string) []AnalyzedPackage {
	f := finder.New(projectRoot)
	defer f.Close()

	var wg sync.WaitGroup
	var mu sync.Mutex
	semaphore := make(chan struct{}, analyzeConcurrency)
	analyzed := make([]AnalyzedPackage, 0, len(requires))

	for _, req := range requires {
		wg.Add(1)
		go func(r parser.Require) {
			defer wg.Done()

			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			info, err := f.FindCompatibleVersions(r.Path, r.Version, targetGoVersion)
			pkg := AnalyzedPackage{
				Pkg:    r.Path,
				Info:   info,
				Err:    err,
				Status: classify(info, err, r.Version),
			}

			mu.Lock()
			analyzed = append(analyzed, pkg)
			mu.Unlock()
		}(req)
	}

	wg.Wait()

	sort.Slice(analyzed, func(i, j int) bool { return analyzed[i].Pkg < analyzed[j].Pkg })
	return analyzed
}

// FindCompatibleVersionsParallel finds compatible versions for all direct
// dependencies. Convenience wrapper for the non-TUI CLI modes.
func FindCompatibleVersionsParallel(requires []parser.Require, targetGoVersion, projectRoot string) []*finder.ModuleInfo {
	var directRequires []parser.Require
	for _, req := range requires {
		if !req.Indirect {
			directRequires = append(directRequires, req)
		}
	}

	return extractUpdatable(analyzePackagesParallel(directRequires, targetGoVersion, projectRoot))
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

// TUI runs the interactive application. It applies the updates itself (via the
// updating screen), so it reports only whether the session ran cleanly.
//
// It deliberately returns no module list: it used to hand back the selected
// modules, and the caller applied them a second time on top of the update the
// TUI had already performed.
func TUI(targetGoVersion string, goMod *parser.GoMod, projectRoot string) error {
	m := InitialAppModel(targetGoVersion, goMod, projectRoot)

	// Signal shutdown once Run returns, so any worker still in flight stops
	// instead of blocking forever on a channel the event loop no longer reads.
	// Without this, quitting mid-analysis leaked the finder's scratch directory
	// and orphaned its `go list` children.
	defer close(m.quit)

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("error running TUI: %w", err)
	}
	return nil
}
