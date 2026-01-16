package ui

import (
	"fmt"

	"go-mod-select/pkg/config"
	"go-mod-select/pkg/finder"
	"go-mod-select/pkg/logger"
	"go-mod-select/pkg/parser"

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
)

// SelectableModule represents a module that can be selected in the UI
type SelectableModule struct {
	Info     *finder.ModuleInfo
	Selected bool
	Updated  bool // Whether this module has been updated
}

// Model for the full TUI application
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
	detailViewport  viewport.Model // Viewport for scrollable detail popup

	// Progress state
	spinner          spinner.Model
	progress         progress.Model
	analyzingStatus  string
	analyzingPackage string // Current package being analyzed
	analyzingCount   int    // Number of packages analyzed
	analyzingTotal   int    // Total packages to analyze
	analyzingLogOffset int  // Scroll offset for analysis logs
	updatingStatus   string
	currentModule    int
	totalModules     int

	// Update subscription
	updateSub chan tea.Msg // Channel for receiving update progress

	// Analysis subscription
	analysisSub chan tea.Msg // Channel for receiving real-time progress updates

	// Logs - separate logs for different screens
	analysisLogs  []string // Full logs for analysis screen (all logs, scrollable)
	operationLogs []string // Operation logs for selection screen (last 3 only)
	updateLogs    []string // Update logs for updating screen (all logs, scrollable)

	// UI dimensions
	width  int
	height int
	ready  bool

	// Result
	confirmed bool
	cancelled bool
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

type keyMap struct {
	Up       key.Binding
	Down     key.Binding
	Space    key.Binding
	Enter    key.Binding
	Tab      key.Binding
	Filter   key.Binding
	Esc      key.Binding
	Quit     key.Binding
	PageUp   key.Binding
	PageDown key.Binding
}

var keys = keyMap{
	Up: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑/k", "up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓/j", "down"),
	),
	Space: key.NewBinding(
		key.WithKeys(" "),
		key.WithHelp("space", "toggle"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "confirm"),
	),
	Tab: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "switch tab"),
	),
	Filter: key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "filter"),
	),
	Esc: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "cancel"),
	),
	Quit: key.NewBinding(
		key.WithKeys("ctrl+c", "q"),
		key.WithHelp("q/ctrl+c", "quit"),
	),
	PageUp: key.NewBinding(
		key.WithKeys("pgup"),
	),
	PageDown: key.NewBinding(
		key.WithKeys("pgdown"),
	),
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

type updateCompleteMsg struct{}

type startUpdateMsg struct {
	modules         []*finder.ModuleInfo
	targetGoVersion string
	goMod           *parser.GoMod
}

func InitialAppModel(targetGoVersion string, goMod *parser.GoMod, projectRoot string) AppModel {
	// Load color configuration
	colors, err := config.LoadOrCreateDefault()
	if err != nil {
		logger.Log.Error("failed to load color config, using defaults", "error", err)
		colors = config.DefaultColorConfig()
	}

	logger.Log.Info("color configuration loaded")

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
		currentTab:      tabUpdate, // Start with Update tab
	}
}

func (m AppModel) Init() tea.Cmd {
	return m.spinner.Tick
}

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
		return m, nil

	case tea.KeyMsg:
		return m.handleKeyPress(msg)

	case startAnalysisMsg:
		// Start the subscription and listen for the first message
		m.analysisSub = make(chan tea.Msg)
		go startAnalysisWorker(msg, m.analysisSub)
		return m, waitForAnalysisMsg(m.analysisSub)

	case startUpdateMsg:
		// Start the update subscription and listen for the first message
		m.updateSub = make(chan tea.Msg)
		go startUpdateWorker(msg, m.updateSub)
		return m, waitForUpdateMsg(m.updateSub)

	case analyzeCompleteMsg:
		// Close subscription when analysis is complete
		if m.analysisSub != nil {
			close(m.analysisSub)
			m.analysisSub = nil
		}
		return m.handleAnalyzeComplete(msg)

	case analyzeProgressMsg:
		m.analyzingTotal = msg.total

		// Add log entry to analysis logs based on status with color tags
		logEntry := ""
		switch msg.status {
		case "checking":
			// Log checking status with progress indicator
			logEntry = fmt.Sprintf("[CHECKING]%s", msg.packageName)
			m.analyzingPackage = msg.packageName
		case "found":
			logEntry = fmt.Sprintf("[FOUND]%s", msg.packageName)
			// Update count only on completion
			if msg.current > m.analyzingCount {
				m.analyzingCount = msg.current
			}
		case "skipped":
			logEntry = fmt.Sprintf("[SKIPPED]%s", msg.packageName)
			// Update count only on completion
			if msg.current > m.analyzingCount {
				m.analyzingCount = msg.current
			}
		case "error":
			logEntry = fmt.Sprintf("[ERROR]%s", msg.packageName)
			// Update count only on completion
			if msg.current > m.analyzingCount {
				m.analyzingCount = msg.current
			}
		}
		if logEntry != "" {
			m.updateOrAddAnalysisLog(logEntry)
		}

		// Continue listening for more progress messages
		if m.analysisSub != nil {
			return m, waitForAnalysisMsg(m.analysisSub)
		}
		return m, nil

	case updateProgressMsg:
		m.currentModule = msg.current
		m.totalModules = msg.total
		m.updatingStatus = msg.module

		// Add log entry based on status
		logEntry := ""
		switch msg.status {
		case "updating":
			if msg.module == "go mod tidy" {
				logEntry = "⏳ Running go mod tidy..."
			} else {
				logEntry = fmt.Sprintf("⏳ [%d/%d] Updating %s...", msg.current, msg.total, msg.module)
			}
		case "success":
			if msg.module == "go mod tidy" {
				logEntry = "✓ `go mod tidy` completed"
			} else {
				logEntry = fmt.Sprintf("✓ [%d/%d] %s updated successfully", msg.current, msg.total, msg.module)
			}
		case "error":
			if msg.module == "go mod tidy" {
				logEntry = "✗ `go mod tidy` failed"
			} else {
				logEntry = fmt.Sprintf("✗ [%d/%d] %s update failed", msg.current, msg.total, msg.module)
			}
		case "tidying":
			logEntry = "⏳ Running `go mod tidy`..."
		}
		if logEntry != "" {
			m.updateLogs = append(m.updateLogs, logEntry)
		}

		// Continue listening for more update messages
		if m.updateSub != nil {
			return m, waitForUpdateMsg(m.updateSub)
		}
		return m, nil

	case updateCompleteMsg:
		// Close subscription when update is complete
		if m.updateSub != nil {
			close(m.updateSub)
			m.updateSub = nil
		}
		m.currentScreen = screenComplete
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

func (m AppModel) handleKeyPress(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Global quit
	if key.Matches(msg, keys.Quit) {
		m.cancelled = true
		return m, tea.Quit
	}

	switch m.currentScreen {
	case screenWelcome:
		if key.Matches(msg, keys.Enter) {
			m.currentScreen = screenAnalyzing
			// Initialize analysis counters
			m.analyzingTotal = countDirectDeps(m.goMod.Requires)
			m.analyzingCount = 0
			m.analyzingPackage = ""
			m.analyzingLogOffset = 0 // Reset scroll offset
			return m, tea.Batch(
				m.spinner.Tick,
				m.analyzeModules(),
			)
		}

	case screenAnalyzing:
		// Handle scrolling in analysis logs
		switch {
		case key.Matches(msg, keys.Up):
			if m.analyzingLogOffset > 0 {
				m.analyzingLogOffset--
			}
			return m, nil
		case key.Matches(msg, keys.Down):
			// Allow scrolling down if there are hidden logs
			logCount := len(m.analysisLogs)
			if logCount > m.getAnalyzingLogVisibleLines() {
				maxOffset := logCount - m.getAnalyzingLogVisibleLines()
				if m.analyzingLogOffset < maxOffset {
					m.analyzingLogOffset++
				}
			}
			return m, nil
		case key.Matches(msg, keys.PageUp):
			m.analyzingLogOffset = max(0, m.analyzingLogOffset-10)
			return m, nil
		case key.Matches(msg, keys.PageDown):
			logCount := len(m.analysisLogs)
			if logCount > m.getAnalyzingLogVisibleLines() {
				maxOffset := logCount - m.getAnalyzingLogVisibleLines()
				m.analyzingLogOffset = min(maxOffset, m.analyzingLogOffset+10)
			}
			return m, nil
		}

	case screenSelection:
		return m.handleSelectionInput(msg)

	case screenComplete:
		if key.Matches(msg, keys.Enter) {
			// Mark updated modules and return to selection screen
			for _, mod := range m.updateModules {
				if mod.Selected {
					mod.Updated = true
					mod.Selected = false // Deselect after update
				}
			}

			// Return to selection screen
			m.currentScreen = screenSelection
			m.cursor = 0
			m.updateViewport()
			return m, nil
		}
		if key.Matches(msg, keys.Quit) {
			return m, tea.Quit
		}
	}

	return m, nil
}

func (m AppModel) handleSelectionInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// If showing detail popup, handle scrolling and closing
	if m.showingDetail {
		switch {
		case key.Matches(msg, keys.Esc):
			m.showingDetail = false
			m.detailContent = ""
			return m, nil
		case key.Matches(msg, keys.Up):
			m.detailViewport.LineUp(1)
			return m, nil
		case key.Matches(msg, keys.Down):
			m.detailViewport.LineDown(1)
			return m, nil
		case key.Matches(msg, keys.PageUp):
			m.detailViewport.ViewUp()
			return m, nil
		case key.Matches(msg, keys.PageDown):
			m.detailViewport.ViewDown()
			return m, nil
		}
		// Ignore other keys when showing detail
		return m, nil
	}

	// Only allow selection in Update tab
	canSelect := m.currentTab == tabUpdate

	if m.filterMode {
		return m.handleFilterInput(msg)
	}

	switch {
	case key.Matches(msg, keys.Enter):
		if canSelect {
			// Check if any modules are selected
			selectedCount := 0
			for _, mod := range m.updateModules {
				if mod.Selected && !mod.Updated {
					selectedCount++
				}
			}

			if selectedCount == 0 {
				// No modules selected, show message and don't proceed
				m.addOperationLog("No modules selected for update")
				return m, nil
			}

			m.confirmed = true
			m.currentScreen = screenUpdating
			return m, m.performUpdate()
		}

	case key.Matches(msg, keys.Tab):
		m.currentTab = (m.currentTab + 1) % 4 // Now we have 4 tabs
		m.cursor = 0
		m.updateViewport()

	case key.Matches(msg, keys.Filter):
		m.filterMode = true
		m.filterInput = ""
		m.addOperationLog("Filter mode - type to search")

	case key.Matches(msg, keys.Space):
		if canSelect && len(m.updateModules) > 0 && m.cursor < len(m.updateModules) {
			// Check if this module has been updated
			visibleModules := m.getVisibleUpdateModules()
			if m.cursor >= len(visibleModules) {
				return m, nil
			}
			actualIdx := visibleModules[m.cursor]

			if m.updateModules[actualIdx].Updated {
				// Already updated, cannot select
				m.addOperationLog(fmt.Sprintf("Already updated: %s", m.updateModules[actualIdx].Info.Path))
				return m, nil
			}

			// Update tab: toggle selection
			m.updateModules[actualIdx].Selected = !m.updateModules[actualIdx].Selected
			status := "selected"
			if !m.updateModules[actualIdx].Selected {
				status = "deselected"
			}
			m.addOperationLog(fmt.Sprintf("%s: %s", status, m.updateModules[actualIdx].Info.Path))
			m.updateViewport()
		} else {
			// Read-only tabs: show detail popup
			m.detailContent = m.getDetailContent()

			if len(m.detailContent) == 0 {
				// Skip logging for empty details
				return m, nil
			}

			m.showingDetail = true

			// Initialize detail viewport with content
			// Leave enough space for border (2) and padding (4)
			popupWidth := min(m.width-10, 80)
			popupHeight := min(m.height-10, 30)
			viewportWidth := popupWidth - 6   // Border (2) + Padding (4)
			viewportHeight := popupHeight - 4 // Border (2) + Padding (2)

			m.detailViewport = viewport.New(viewportWidth, viewportHeight)
			m.detailViewport.SetContent(m.detailContent)
		}

	case key.Matches(msg, keys.Up):
		if m.cursor > 0 {
			m.cursor--
			m.updateViewport()
			m.syncViewportToCursor()
		}

	case key.Matches(msg, keys.Down):
		maxCursor := m.getMaxCursor()
		if m.cursor < maxCursor {
			m.cursor++
			m.updateViewport()
			m.syncViewportToCursor()
		}

	case key.Matches(msg, keys.PageUp):
		m.cursor = max(0, m.cursor-10)
		m.updateViewport()
		m.syncViewportToCursor()

	case key.Matches(msg, keys.PageDown):
		maxCursor := m.getMaxCursor()
		m.cursor = min(maxCursor, m.cursor+10)
		m.updateViewport()
		m.syncViewportToCursor()
	}

	return m, nil
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
		m.addOperationLog(fmt.Sprintf("Filter: '%s'", m.filterInput))
		m.cursor = 0
		m.updateViewport()

	case "backspace":
		if len(m.filterInput) > 0 {
			m.filterInput = m.filterInput[:len(m.filterInput)-1]
			m.updateViewport()
		}

	default:
		if len(msg.String()) == 1 {
			m.filterInput += msg.String()
			m.updateViewport()
		}
	}

	return m, nil
}

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

// Implement remaining methods in next file...
