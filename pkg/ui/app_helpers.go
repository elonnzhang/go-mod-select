package ui

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"go-mod-select/pkg/analyzer"
	"go-mod-select/pkg/finder"
	"go-mod-select/pkg/parser"
	"go-mod-select/pkg/updater"

	tea "github.com/charmbracelet/bubbletea"
)

// addAnalysisLog adds a log entry for the analysis screen (no limit, all logs kept)
func (m *AppModel) addAnalysisLog(msg string) {
	m.analysisLogs = append(m.analysisLogs, msg)
}

// updateOrAddAnalysisLog updates an existing log entry or adds a new one
// This is used to replace [CHECKING] status with final status (FOUND/SKIPPED/ERROR)
func (m *AppModel) updateOrAddAnalysisLog(msg string) {
	// Extract package name from the message
	var packageName string
	if strings.HasPrefix(msg, "[FOUND]") {
		packageName = strings.TrimPrefix(msg, "[FOUND]")
	} else if strings.HasPrefix(msg, "[SKIPPED]") {
		packageName = strings.TrimPrefix(msg, "[SKIPPED]")
	} else if strings.HasPrefix(msg, "[ERROR]") {
		packageName = strings.TrimPrefix(msg, "[ERROR]")
	} else {
		// For CHECKING status, just add
		m.analysisLogs = append(m.analysisLogs, msg)
		return
	}

	// Try to find and replace existing CHECKING entry for this package
	checkingMsg := "[CHECKING]" + packageName
	for i := len(m.analysisLogs) - 1; i >= 0; i-- {
		if m.analysisLogs[i] == checkingMsg {
			m.analysisLogs[i] = msg
			return
		}
	}

	// If not found, just add the new entry
	m.analysisLogs = append(m.analysisLogs, msg)
}

// addOperationLog adds a log entry for the selection screen (keep last 3 only)
func (m *AppModel) addOperationLog(msg string) {
	m.operationLogs = append(m.operationLogs, msg)
	if len(m.operationLogs) > 3 {
		m.operationLogs = m.operationLogs[len(m.operationLogs)-3:]
	}
}

// getDetailContent returns the content for the detail pane
func (m *AppModel) getDetailContent() string {
	var content strings.Builder

	switch m.currentTab {
	case tabRequire:
		if m.cursor >= 0 && m.cursor < len(m.requireModules) {
			req := m.requireModules[m.cursor]
			content.WriteString("📦 Require Directive Details\n\n")
			content.WriteString(fmt.Sprintf("Module Path:  %s\n", req.Path))
			content.WriteString(fmt.Sprintf("Version:      %s\n", req.Version))
			content.WriteString("Type:         Direct dependency\n")

			// Find which files use this package
			content.WriteString("\n🔍 Used in files:\n")
			files, err := analyzer.FindFilesUsingPackage(m.projectRoot, req.Path)
			if err != nil {
				content.WriteString(fmt.Sprintf("  (Error analyzing: %v)\n", err))
			} else if len(files) == 0 {
				content.WriteString("  (No files found - may be used transitively)\n")
			} else {
				for _, file := range files {
					content.WriteString(fmt.Sprintf("  • %s\n", file))
				}
			}
		}

	case tabIndirect:
		if m.cursor >= 0 && m.cursor < len(m.indirectModules) {
			req := m.indirectModules[m.cursor]
			content.WriteString("📦 Indirect Require Details\n\n")
			content.WriteString(fmt.Sprintf("Module Path:  %s\n", req.Path))
			content.WriteString(fmt.Sprintf("Version:      %s\n", req.Version))
			content.WriteString("Type:         Indirect dependency\n")

			// Find why this dependency is needed
			content.WriteString("\n🔗 Required by:\n")
			reasons := analyzer.FindWhyPackageNeeded(m.projectRoot, req.Path)
			if len(reasons) == 0 {
				content.WriteString("  (Analyzing dependency chain...)\n")
			} else {
				for _, reason := range reasons {
					content.WriteString(fmt.Sprintf("  %s\n", reason))
				}
			}

			content.WriteString("\n💡 Indirect dependencies are modules that your dependencies depend on, but your module doesn't directly import.\n")
		}

	case tabReplace:
		if m.cursor >= 0 && m.cursor < len(m.replaceModules) {
			rep := m.replaceModules[m.cursor]
			content.WriteString("🔄 Replace Directive Details\n\n")
			content.WriteString(fmt.Sprintf("Original:     %s\n", rep.Old))
			content.WriteString(fmt.Sprintf("Replacement:  %s\n", rep.New))
			content.WriteString("\nReplace directives allow you to override\n")
			content.WriteString("module dependencies with local or forked\n")
			content.WriteString("versions.\n")
		}

	default:
		content.WriteString("No details available")
	}

	return content.String()
}

func (m *AppModel) updateViewport() {
	if !m.ready {
		return
	}
	content := m.renderModuleList()
	m.viewport.SetContent(content)
}

func (m *AppModel) syncViewportToCursor() {
	if !m.ready {
		return
	}

	lineHeight := 1
	cursorLine := m.cursor * lineHeight

	viewportTop := m.viewport.YOffset
	viewportBottom := viewportTop + m.viewport.Height - 1

	if cursorLine > viewportBottom {
		m.viewport.LineDown(cursorLine - viewportBottom)
	}

	if cursorLine < viewportTop {
		m.viewport.LineUp(viewportTop - cursorLine)
	}
}

func (m AppModel) getHeaderHeight() int {
	return 8 // Title + colors + help + tabs + borders
}

func (m AppModel) getFooterHeight() int {
	return 6 // Separator + title + 3 logs
}

func (m AppModel) getViewportHeight() int {
	return m.height - m.getHeaderHeight() - m.getFooterHeight()
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
		}
	}
}

// startAnalysisMsg is the message to begin analysis
type startAnalysisMsg struct {
	directRequires   []parser.Require
	indirectRequires []parser.Require
	targetGoVersion  string
	goMod            *parser.GoMod
}

// waitForAnalysisMsg creates a command that waits for the next message from the subscription
func waitForAnalysisMsg(sub <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-sub
	}
}

// startAnalysisWorker performs the analysis and sends progress updates through the channel
func startAnalysisWorker(msg startAnalysisMsg, sub chan<- tea.Msg) {
	total := len(msg.directRequires)

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 200) // Increase concurrency to 200

	// Channels for collecting results
	progressChan := make(chan AnalyzedPackage, total)

	// Track completion count using atomic operations
	var completed int32

	for _, req := range msg.directRequires {
		wg.Add(1)
		go func(r parser.Require) {
			defer wg.Done()

			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// Send "checking" progress (before incrementing counter)
			sub <- analyzeProgressMsg{
				packageName: r.Path,
				current:     int(atomic.LoadInt32(&completed)),
				total:       total,
				status:      "checking",
			}

			// Perform the actual analysis
			info, err := finder.FindCompatibleVersions(r.Path, r.Version, msg.targetGoVersion)
			status := "skipped"
			if err != nil {
				status = "error"
			} else if info != nil && info.LatestCompatible != r.Version {
				status = "found"
			}

			pkg := AnalyzedPackage{
				Pkg:    r.Path,
				Info:   info,
				Err:    err,
				Status: status,
			}

			// Atomically increment counter after completion
			current := int(atomic.AddInt32(&completed, 1))

			// Send completion progress with status
			sub <- analyzeProgressMsg{
				packageName: r.Path,
				current:     current,
				total:       total,
				status:      status,
			}

			// Store result
			progressChan <- pkg
		}(req)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(progressChan)

	// Collect all results
	completedPackages := make([]AnalyzedPackage, 0, total)
	for pkg := range progressChan {
		completedPackages = append(completedPackages, pkg)
	}

	// Extract modules with available updates
	var requireModules []*finder.ModuleInfo
	for _, pkg := range completedPackages {
		if pkg.Err == nil && pkg.Info != nil && pkg.Info.LatestCompatible != pkg.Info.CurrentVersion {
			requireModules = append(requireModules, pkg.Info)
		}
	}

	// Extract replace directives
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

	// Send final completion message
	sub <- analyzeCompleteMsg{
		requireModules:    requireModules,
		indirectModules:   msg.indirectRequires,
		replaces:          replaces,
		completedPackages: completedPackages,
	}
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
			if mod.Selected {
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

// startUpdateWorker performs the update and sends progress updates through the channel
func startUpdateWorker(msg startUpdateMsg, sub chan<- tea.Msg) {
	updater := updater.NewUpdater(msg.goMod)

	// Progress callback function
	progressCb := func(current, total int, module, status string) {
		sub <- updateProgressMsg{
			current: current,
			total:   total,
			module:  module,
			status:  status,
		}
	}

	// Perform update with progress reporting
	err := updater.UpdateAllWithProgress(msg.targetGoVersion, msg.modules, false, progressCb)

	if err != nil {
		// Send error in log format
		sub <- updateProgressMsg{
			current: 0,
			total:   0,
			module:  fmt.Sprintf("Error: %v", err),
			status:  "error",
		}
	}

	// Send completion message
	sub <- updateCompleteMsg{}
}

// analyzePackagesParallel analyzes packages in parallel and returns detailed results
// This is the internal implementation used by both TUI and CLI modes
func analyzePackagesParallel(requires []parser.Require, targetGoVersion string) []AnalyzedPackage {
	var wg sync.WaitGroup
	results := make(chan AnalyzedPackage, len(requires))
	semaphore := make(chan struct{}, 100)

	for _, req := range requires {
		wg.Add(1)
		go func(r parser.Require) {
			defer wg.Done()

			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			info, err := finder.FindCompatibleVersions(r.Path, r.Version, targetGoVersion)
			status := "skipped"
			if err != nil {
				status = "error"
			} else if info != nil && info.LatestCompatible != r.Version {
				status = "found"
			}
			results <- AnalyzedPackage{
				Pkg:    r.Path,
				Info:   info,
				Err:    err,
				Status: status,
			}
		}(req)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var analyzedPackages []AnalyzedPackage
	for pkg := range results {
		analyzedPackages = append(analyzedPackages, pkg)
	}

	return analyzedPackages
}

// FindCompatibleVersionsParallel finds compatible versions for all direct dependencies
// This is a convenience wrapper for external use (CLI mode)
func FindCompatibleVersionsParallel(requires []parser.Require, targetGoVersion string, verbose ...bool) []*finder.ModuleInfo {
	// Filter out indirect dependencies for display
	var directRequires []parser.Require
	for _, req := range requires {
		if req.Indirect {
			fmt.Printf("[Info] Skipping indirect module: %s \n", req.Path)
		} else {
			directRequires = append(directRequires, req)
		}
	}

	// Use the shared parallel analysis function
	analyzedPackages := analyzePackagesParallel(directRequires, targetGoVersion)

	// Extract only modules with available updates
	var moduleInfos []*finder.ModuleInfo
	for _, pkg := range analyzedPackages {
		if pkg.Err == nil && pkg.Info != nil && pkg.Info.LatestCompatible != pkg.Info.CurrentVersion {
			moduleInfos = append(moduleInfos, pkg.Info)
		}
	}

	return moduleInfos
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// getAnalyzingLogVisibleLines returns the number of visible lines in analyzing log area
func (m AppModel) getAnalyzingLogVisibleLines() int {
	logBoxHeight := m.height / 2
	if logBoxHeight < 10 {
		logBoxHeight = 10
	}
	// title(1) + separator(1) + padding(2 top+bottom) = 4 lines overhead
	logContentHeight := logBoxHeight - 4
	if logContentHeight < 3 {
		logContentHeight = 3
	}
	return logContentHeight
}

// RunFullTUI runs the complete TUI application
func RunFullTUI(targetGoVersion string, goMod *parser.GoMod, projectRoot string) ([]*finder.ModuleInfo, error) {
	m := InitialAppModel(targetGoVersion, goMod, projectRoot)
	p := tea.NewProgram(m, tea.WithAltScreen())

	finalModel, err := p.Run()
	if err != nil {
		return nil, fmt.Errorf("error running TUI: %w", err)
	}

	result := finalModel.(AppModel)

	if result.cancelled {
		return nil, nil
	}

	if !result.confirmed {
		return nil, nil
	}

	// Collect selected modules
	var selected []*finder.ModuleInfo
	for _, mod := range result.updateModules {
		if mod.Selected {
			selected = append(selected, mod.Info)
		}
	}

	return selected, nil
}
