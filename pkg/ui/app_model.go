package ui

import (
	"fmt"
	"strings"

	"go-mod-select/pkg/config"
	"go-mod-select/pkg/finder"
	"go-mod-select/pkg/logger"
	"go-mod-select/pkg/parser"
	"go-mod-select/pkg/updater"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Screen types
type screen int

const (
	screenWelcome screen = iota
	screenAnalyzing
	screenSelection
	screenUpdating
	screenComplete
)

// Tab types
type tabType int

const (
	tabUpdate   tabType = iota // Can update - shows modules with available updates
	tabRequire                 // Read-only - shows current require directives
	tabIndirect                // Read-only - shows current indirect requires
	tabReplace                 // Read-only - shows current replace directives
	tabCount                   // sentinel: number of tabs, used for cycling
)

// SelectableModule represents a module that can be selected in the UI
type SelectableModule struct {
	Info     *finder.ModuleInfo
	Selected bool
	Updated  bool // Whether this module has been updated
}

// AppModel for the full TUI application
// implement tea.Model interface
type AppModel struct {
	// App state
	currentScreen   screen
	targetGoVersion string
	goMod           *parser.GoMod
	projectRoot     string // Project root directory for file analysis

	// Color configuration
	colors *config.ColorConfig

	// Selection screen state
	updateModules   []*SelectableModule // Modules with available updates (can select)
	requireModules  []parser.Require    // Current require directives (read-only)
	indirectModules []parser.Require    // Current indirect requires (read-only)
	replaceModules  []ReplaceInfo       // Current replace directives (read-only)
	currentTab      tabType
	cursor          int
	viewport        viewport.Model
	filterMode      bool
	filterInput     string
	showingDetail   bool           // Whether showing detail popup
	detailContent   string         // Content of detail popup
	detailModule    string         // Module the popup describes, to drop stale async results
	detailViewport  viewport.Model // Viewport for scrollable detail popup

	// Progress state
	spinner            spinner.Model
	progress           progress.Model
	analyzingPackage   string // Current package being analyzed
	analyzingCount     int    // Number of packages analyzed
	analyzingTotal     int    // Total packages to analyze
	analyzingLogOffset int    // Scroll offset for analysis logs
	analyzingLogFollow bool   // Auto-scroll to newest log until the user scrolls up
	currentModule      int
	totalModules       int
	tidySucceeded      bool   // go mod tidy reported success in this update run
	tidyRunning        bool   // go mod tidy is in flight (not a per-module step)
	updateFailed       bool   // at least one step of the update run failed
	updateError        error  // why the update run failed, if it did
	actualGoVersion    string // go directive found on disk after the update
	goVersionMatched   bool   // whether that matches what was requested

	// Update subscription
	updateSub chan tea.Msg // Channel for receiving update progress

	// Analysis subscription
	analysisSub chan tea.Msg // Channel for receiving real-time progress updates

	// quit is closed when the program is shutting down. Background workers
	// select on it so they never block sending to a channel nobody reads.
	quit chan struct{}

	// Logs - separate logs for different screens
	analysisLogs  []string // Full logs for analysis screen (all logs, scrollable)
	operationLogs []string // Operation logs for selection screen (last 3 only)
	updateLogs    []string // Update logs for updating screen (all logs, scrollable)

	// UI dimensions
	width  int
	height int
	ready  bool
}

type ReplaceInfo struct {
	Old string
	New string
}

type AnalyzedPackage struct {
	Pkg    string
	Info   *finder.ModuleInfo
	Err    error
	Status string // "found", "skipped", "error"
}

// Messages
type analyzeCompleteMsg struct {
	requireModules    []*finder.ModuleInfo
	indirectModules   []parser.Require
	replaces          []ReplaceInfo
	completedPackages []AnalyzedPackage
}

type analyzeProgressMsg struct {
	packageName string
	current     int
	total       int
	status      string // "checking", "found", "skipped", "error"
}

type updateProgressMsg struct {
	current int
	total   int
	module  string
	status  string // "updating", "success", "error", "tidying"
}

// updateCompleteMsg ends an update run. actualGoVersion is the go directive
// found on disk afterwards, which `go mod tidy` may have raised above the
// requested one.
type updateCompleteMsg struct {
	actualGoVersion  string
	goVersionMatched bool
}

// updateFailedMsg carries the reason an update run aborted. The updater has
// already restored go.mod and go.sum by the time this is sent.
type updateFailedMsg struct{ err error }

// detailLoadedMsg delivers the slow half of a detail popup once the source walk
// or `go mod why` has finished.
type detailLoadedMsg struct {
	module string
	body   string
}

type startUpdateMsg struct {
	modules         []*finder.ModuleInfo
	targetGoVersion string
	goMod           *parser.GoMod
}

// InitialAppModel creates a new AppModel with default values
func InitialAppModel(targetGoVersion string, goMod *parser.GoMod, projectRoot string) AppModel {
	// A broken or unwritable theme file degrades to the defaults rather than
	// failing the run; LoadOrCreateDefault returns a usable config either way.
	colors, err := config.LoadOrCreateDefault()
	if err != nil {
		logger.Log.Warn("color config unavailable, using defaults", "error", err)
	}
	if colors == nil {
		colors = config.DefaultColorConfig()
	}

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(colors.ToLipglossColor(colors.Title))

	// Create progress bar with theme colors
	p := progress.New(
		progress.WithSolidFill(string(colors.ToLipglossColor(colors.Success))),
		progress.WithoutPercentage(),
	)

	return AppModel{
		currentScreen:   screenWelcome,
		targetGoVersion: targetGoVersion,
		goMod:           goMod,
		projectRoot:     projectRoot,
		colors:          colors,
		spinner:         s,
		progress:        p,
		analysisLogs:    []string{},
		operationLogs:   []string{},
		updateLogs:      []string{},
		quit:            make(chan struct{}),
		currentTab:      tabUpdate, // Start with Update tab
	}
}

// Implement the tea.Model interface
func (m AppModel) Init() tea.Cmd {
	return m.spinner.Tick
}

// Implement the tea.Model interface
func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		if !m.ready && m.currentScreen == screenSelection {
			m.viewport = viewport.New(msg.Width, m.getViewportHeight())
			m.viewport.YPosition = m.getHeaderHeight()
			m.ready = true
			m.updateViewport()
		} else if m.ready {
			m.viewport.Width = msg.Width
			m.viewport.Height = m.getViewportHeight()
			m.updateViewport()
		}

		// An open popup must be resized too, or the border is drawn at the new
		// width around content laid out for the old one.
		if m.showingDetail {
			w, h := m.detailViewportSize()
			m.detailViewport.Width = w
			m.detailViewport.Height = h
			m.detailViewport.SetContent(m.detailContent)
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKeyPress(msg)

	case startAnalysisMsg:
		m.analysisSub = make(chan tea.Msg)
		go startAnalysisWorker(msg, m.analysisSub, m.quit)
		return m, waitForAnalysisMsg(m.analysisSub)

	case startUpdateMsg:
		m.updateSub = make(chan tea.Msg)
		go startUpdateWorker(msg, m.updateSub, m.quit)
		return m, waitForUpdateMsg(m.updateSub)

	case analyzeCompleteMsg:
		// Drop the reference rather than close(): the worker is the only sender
		// and it has already finished, so closing buys nothing and would panic
		// if any future path sent again.
		m.analysisSub = nil
		return m.handleAnalyzeComplete(msg)

	case analyzeProgressMsg:
		m.analyzingTotal = msg.total

		if msg.status == statusChecking {
			m.analyzingPackage = msg.packageName
		} else if msg.current > m.analyzingCount {
			// Only completions advance the counter; a "checking" message is
			// emitted before its work happens.
			m.analyzingCount = msg.current
		}
		m.updateOrAddAnalysisLog(msg.status, msg.packageName)

		// Keep the log pinned to the newest entry unless the user scrolled up
		// to read history.
		if m.analyzingLogFollow {
			m.analyzingLogOffset = m.maxAnalyzingLogOffset()
		}

		// Continue listening for more progress messages
		if m.analysisSub != nil {
			return m, waitForAnalysisMsg(m.analysisSub)
		}
		return m, nil

	case updateProgressMsg:
		isTidy := msg.module == updater.TidyLabel

		// The tidy step reports 0/0 because it is not per-module. Assigning
		// that would zero the counters and hide the progress bar during the
		// slowest step of the run, so leave them at the module totals.
		if !isTidy {
			m.currentModule = msg.current
			m.totalModules = msg.total
		}

		logEntry := ""
		switch msg.status {
		case "updating":
			if isTidy {
				logEntry = "⏳ Running `go mod tidy`..."
				m.tidyRunning = true
			} else {
				logEntry = fmt.Sprintf("⏳ [%d/%d] Updating %s...", msg.current, msg.total, msg.module)
			}
		case "success":
			if isTidy {
				logEntry = "✓ `go mod tidy` completed"
				m.tidySucceeded = true
				m.tidyRunning = false
			} else {
				logEntry = fmt.Sprintf("✓ [%d/%d] %s updated successfully", msg.current, msg.total, msg.module)
				// Mark the module updated here, while we know which one
				// succeeded. The completion screen reads this, so deriving it
				// by re-parsing log strings later is both fragile and — since
				// it ran before the flag was set — produced an empty summary.
				m.markUpdated(msg.module)
			}
		case "error":
			if isTidy {
				logEntry = "✗ `go mod tidy` failed"
				m.tidyRunning = false
			} else {
				logEntry = fmt.Sprintf("✗ [%d/%d] %s update failed", msg.current, msg.total, msg.module)
			}
			m.updateFailed = true
		case "tidying":
			logEntry = "⏳ Running `go mod tidy`..."
			m.tidyRunning = true
		}
		if logEntry != "" {
			m.updateLogs = append(m.updateLogs, logEntry)
		}

		// Continue listening for more update messages
		if m.updateSub != nil {
			return m, waitForUpdateMsg(m.updateSub)
		}
		return m, nil

	case updateFailedMsg:
		m.updateFailed = true
		m.updateError = msg.err
		m.updateLogs = append(m.updateLogs,
			fmt.Sprintf("✗ %v", msg.err),
			"↩ go.mod and go.sum restored to their previous contents")

		if m.updateSub != nil {
			return m, waitForUpdateMsg(m.updateSub)
		}
		return m, nil

	case updateCompleteMsg:
		// Drop the reference rather than close() — see analyzeCompleteMsg.
		m.updateSub = nil
		m.actualGoVersion = msg.actualGoVersion
		m.goVersionMatched = msg.goVersionMatched
		if m.updateFailed {
			// A rollback undid every applied version, so nothing may be shown
			// as updated on the summary screen.
			for _, mod := range m.updateModules {
				mod.Updated = false
			}
		}
		m.currentScreen = screenComplete
		return m, nil

	case detailLoadedMsg:
		// Discard a late result for a popup the user has already closed or
		// moved on from.
		if !m.showingDetail || msg.module != m.detailModule {
			return m, nil
		}
		m.detailContent = strings.TrimSuffix(m.detailContent, detailLoadingNotice) + msg.body
		m.detailViewport.SetContent(m.detailContent)
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case progress.FrameMsg:
		progressModel, cmd := m.progress.Update(msg)
		m.progress = progressModel.(progress.Model)
		return m, cmd
	}

	return m, nil
}

// handleKeyPress Handle key presses
func (m AppModel) handleKeyPress(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Ctrl+C always quits. Plain "q" is deliberately not global: on the
	// selection screen it has to reach the filter input, where it is a literal
	// character, not a command.
	if key.Matches(msg, keys.ForceQuit) {
		return m, tea.Quit
	}

	switch m.currentScreen {
	case screenWelcome:
		switch {
		case key.Matches(msg, keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, keys.Enter):
			m.currentScreen = screenAnalyzing
			// Initialize analysis counters
			m.analyzingTotal = parser.CountDirectDeps(m.goMod.Requires)
			m.analyzingCount = 0
			m.analyzingPackage = ""
			m.analyzingLogOffset = 0
			m.analyzingLogFollow = true
			return m, tea.Batch(
				m.spinner.Tick,
				m.analyzeModules(),
			)
		}

	case screenAnalyzing:
		// Scroll the analysis log. Scrolling up detaches from the tail so the
		// user can read history while workers keep appending.
		switch {
		case key.Matches(msg, keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, keys.Up):
			if m.analyzingLogOffset > 0 {
				m.analyzingLogOffset--
				m.analyzingLogFollow = false
			}
			return m, nil
		case key.Matches(msg, keys.Down):
			maxOffset := m.maxAnalyzingLogOffset()
			if m.analyzingLogOffset < maxOffset {
				m.analyzingLogOffset++
			}
			m.analyzingLogFollow = m.analyzingLogOffset >= maxOffset
			return m, nil
		case key.Matches(msg, keys.PageUp):
			if m.analyzingLogOffset > 0 {
				m.analyzingLogOffset = max(0, m.analyzingLogOffset-10)
				m.analyzingLogFollow = false
			}
			return m, nil
		case key.Matches(msg, keys.PageDown):
			maxOffset := m.maxAnalyzingLogOffset()
			m.analyzingLogOffset = min(maxOffset, m.analyzingLogOffset+10)
			m.analyzingLogFollow = m.analyzingLogOffset >= maxOffset
			return m, nil
		case key.Matches(msg, keys.End):
			m.analyzingLogOffset = m.maxAnalyzingLogOffset()
			m.analyzingLogFollow = true
			return m, nil
		}

	case screenSelection:
		return m.handleSelectionInput(msg)

	case screenUpdating:
		// The update is mutating go.mod; only a hard interrupt is honoured.
		return m, nil

	case screenComplete:
		switch {
		case key.Matches(msg, keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, keys.Enter):
			// Deselect the modules that were just applied and go back to the
			// list so the user can pick another batch.
			for _, mod := range m.updateModules {
				if mod.Updated {
					mod.Selected = false
				}
			}

			m.currentScreen = screenSelection
			m.cursor = 0
			m.currentTab = tabUpdate
			m.updateError = nil
			m.updateFailed = false
			m.updateViewport()
			return m, nil
		}
	}

	return m, nil
}

func (m AppModel) handleSelectionInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// If showing detail popup, handle scrolling and closing
	if m.showingDetail {
		switch {
		case key.Matches(msg, keys.Esc), key.Matches(msg, keys.Quit):
			m.showingDetail = false
			m.detailContent = ""
			m.detailModule = ""
			return m, nil
		case key.Matches(msg, keys.Up):
			m.detailViewport.ScrollUp(1)
			return m, nil
		case key.Matches(msg, keys.Down):
			m.detailViewport.ScrollDown(1)
			return m, nil
		case key.Matches(msg, keys.PageUp):
			m.detailViewport.HalfPageUp()
			return m, nil
		case key.Matches(msg, keys.PageDown):
			m.detailViewport.HalfPageDown()
			return m, nil
		}
		// Ignore other keys when showing detail
		return m, nil
	}

	// Filter input owns every printable key, including q — checked before the
	// command bindings so typing a module name cannot quit the app.
	if m.filterMode {
		return m.handleFilterInput(msg)
	}

	// Only allow selection in Update tab
	canSelect := m.currentTab == tabUpdate

	switch {
	case key.Matches(msg, keys.Quit):
		return m, tea.Quit

	case key.Matches(msg, keys.Enter):
		if canSelect {
			selectedCount := 0
			for _, mod := range m.updateModules {
				if mod.Selected && !mod.Updated {
					selectedCount++
				}
			}

			if selectedCount == 0 {
				m.addOperationLog("No modules selected for update")
				return m, nil
			}

			m.currentScreen = screenUpdating
			m.updateLogs = nil
			m.tidySucceeded = false
			m.tidyRunning = false
			m.updateFailed = false
			m.currentModule = 0
			m.totalModules = 0
			return m, m.performUpdate()
		}

	case key.Matches(msg, keys.Esc):
		// Clear an applied filter without re-entering filter mode.
		if m.filterInput != "" {
			m.filterInput = ""
			m.cursor = 0
			m.addOperationLog("Filter cleared")
			m.updateViewport()
		}

	case key.Matches(msg, keys.Tab):
		m.currentTab = (m.currentTab + 1) % tabCount
		m.cursor = 0
		m.viewport.GotoTop()
		m.updateViewport()

	case key.Matches(msg, keys.ShiftTab):
		m.currentTab = (m.currentTab + tabCount - 1) % tabCount
		m.cursor = 0
		m.viewport.GotoTop()
		m.updateViewport()

	case key.Matches(msg, keys.Filter):
		m.filterMode = true
		m.filterInput = ""
		m.cursor = 0
		m.addOperationLog("Filter mode - type to search, Enter to apply")
		m.updateViewport()

	case key.Matches(msg, keys.SelectAll):
		if canSelect {
			m.toggleAllVisible()
		}

	case key.Matches(msg, keys.Space):
		if canSelect {
			visible := m.getVisibleUpdateModules()
			if m.cursor < 0 || m.cursor >= len(visible) {
				return m, nil
			}
			actualIdx := visible[m.cursor]
			mod := m.updateModules[actualIdx]

			if mod.Updated {
				m.addOperationLog(fmt.Sprintf("Already updated: %s", mod.Info.Path))
				return m, nil
			}

			mod.Selected = !mod.Selected
			status := "selected"
			if !mod.Selected {
				status = "deselected"
			}
			m.addOperationLog(fmt.Sprintf("%s: %s", status, mod.Info.Path))
			m.updateViewport()
		} else {
			header, ok := m.detailHeader()
			if !ok {
				return m, nil
			}

			// Render the header immediately and fill in the slow part when it
			// arrives, so opening a popup never blocks the event loop.
			load := m.loadDetailBody()
			m.detailModule = m.detailSubject()
			m.detailContent = header
			if load != nil {
				m.detailContent += detailLoadingNotice
			}
			m.showingDetail = true

			// Match renderDetailPopup's geometry: border (2) + horizontal
			// padding (4), border (2) + vertical padding (2), plus one line
			// for the help text below the box.
			w, h := m.detailViewportSize()
			m.detailViewport = viewport.New(w, h)
			m.detailViewport.SetContent(m.detailContent)

			return m, m.loadDetailBody()
		}

	case key.Matches(msg, keys.Up):
		if m.cursor > 0 {
			m.cursor--
			m.updateViewport()
			m.syncViewportToCursor()
		}

	case key.Matches(msg, keys.Down):
		if m.cursor < m.getMaxCursor() {
			m.cursor++
			m.updateViewport()
			m.syncViewportToCursor()
		}

	case key.Matches(msg, keys.PageUp):
		m.cursor = max(0, m.cursor-10)
		m.updateViewport()
		m.syncViewportToCursor()

	case key.Matches(msg, keys.PageDown):
		// max(0, …) matters on an empty tab, where getMaxCursor() is -1: a
		// cursor of -1 sticks (Up needs >0, Down needs <-1) and would index
		// out of range if any consumer trusted it.
		m.cursor = max(0, min(m.getMaxCursor(), m.cursor+10))
		m.updateViewport()
		m.syncViewportToCursor()

	case key.Matches(msg, keys.Home):
		m.cursor = 0
		m.updateViewport()
		m.syncViewportToCursor()

	case key.Matches(msg, keys.End):
		m.cursor = max(0, m.getMaxCursor())
		m.updateViewport()
		m.syncViewportToCursor()
	}

	return m, nil
}

// toggleAllVisible selects every filtered, not-yet-updated module — or clears
// them all if any are already selected, so the key is its own undo.
func (m *AppModel) toggleAllVisible() {
	visible := m.getVisibleUpdateModules()

	anySelected := false
	for _, idx := range visible {
		if m.updateModules[idx].Selected {
			anySelected = true
			break
		}
	}

	changed := 0
	for _, idx := range visible {
		mod := m.updateModules[idx]
		if mod.Updated {
			continue
		}
		if mod.Selected == !anySelected {
			continue
		}
		mod.Selected = !anySelected
		changed++
	}

	if anySelected {
		m.addOperationLog(fmt.Sprintf("Deselected %d module(s)", changed))
	} else {
		m.addOperationLog(fmt.Sprintf("Selected %d module(s)", changed))
	}
	m.updateViewport()
}

func (m AppModel) handleFilterInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filterMode = false
		m.filterInput = ""
		m.cursor = 0
		m.addOperationLog("Filter cancelled")
		m.updateViewport()

	case "enter":
		m.filterMode = false
		if m.filterInput == "" {
			m.addOperationLog("Filter cleared")
		} else {
			m.addOperationLog(fmt.Sprintf("Filter: '%s'", m.filterInput))
		}
		m.cursor = 0
		m.updateViewport()

	case "backspace":
		if m.filterInput != "" {
			// Trim a whole rune: module paths can contain multi-byte
			// characters, and lopping off a single byte would corrupt them.
			runes := []rune(m.filterInput)
			m.filterInput = string(runes[:len(runes)-1])
			m.cursor = 0
			m.updateViewport()
		}

	default:
		if msg.Type == tea.KeyRunes {
			m.filterInput += string(msg.Runes)
			m.cursor = 0
			m.updateViewport()
		} else if msg.Type == tea.KeySpace {
			m.filterInput += " "
			m.cursor = 0
			m.updateViewport()
		}
	}

	return m, nil
}

// View renders the UI. implements tea.Model
func (m AppModel) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	switch m.currentScreen {
	case screenWelcome:
		return m.renderWelcome()
	case screenAnalyzing:
		return m.renderAnalyzing()
	case screenSelection:
		return m.renderSelection()
	case screenUpdating:
		return m.renderUpdating()
	case screenComplete:
		return m.renderComplete()
	}

	return ""
}

// Style helpers - get configured styles
func (m AppModel) titleStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(m.colors.ToLipglossColor(m.colors.Title)).
		Padding(0, 1)
}

func (m AppModel) headerStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(m.colors.ToLipglossColor(m.colors.Border)).
		Padding(0, 1).
		Bold(true)
}

func (m AppModel) tabActiveStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(m.colors.ToLipglossColor(m.colors.TabActive)).
		Background(m.colors.ToLipglossColor(m.colors.TabBackground)).
		Padding(0, 2)
}

func (m AppModel) tabInactiveStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(m.colors.ToLipglossColor(m.colors.TabInactive)).
		Padding(0, 2)
}

func (m AppModel) helpStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(m.colors.ToLipglossColor(m.colors.Help))
}

func (m AppModel) cursorStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(m.colors.ToLipglossColor(m.colors.Cursor))
}

func (m AppModel) selectedStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(m.colors.ToLipglossColor(m.colors.Selected)).
		Bold(true)
}

func (m AppModel) infoStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(m.colors.ToLipglossColor(m.colors.Info)).
		Bold(true)
}

func (m AppModel) successStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(m.colors.ToLipglossColor(m.colors.Success)).
		Bold(true)
}

func (m AppModel) logStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(m.colors.ToLipglossColor(m.colors.Log))
}

func (m AppModel) getVersionStyle(updateType finder.UpdateType) lipgloss.Style {
	switch updateType {
	case finder.Patch:
		return lipgloss.NewStyle().Foreground(m.colors.ToLipglossColor(m.colors.Patch))
	case finder.Minor:
		return lipgloss.NewStyle().Foreground(m.colors.ToLipglossColor(m.colors.Minor))
	case finder.Major:
		return lipgloss.NewStyle().Foreground(m.colors.ToLipglossColor(m.colors.Major))
	case finder.Prerelease:
		return lipgloss.NewStyle().Foreground(m.colors.ToLipglossColor(m.colors.Prerelease))
	default:
		return lipgloss.NewStyle()
	}
}
