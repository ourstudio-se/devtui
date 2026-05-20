package ui

import (
	"os"
	"os/exec"
	"strings"

	"github.com/ourstudio-se/devtui/internal/config"
	"github.com/ourstudio-se/devtui/internal/msgs"
	"github.com/ourstudio-se/devtui/internal/service"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type FocusPanel int

const (
	PanelServiceList FocusPanel = iota
	PanelLogs
)

type Model struct {
	services []*service.Service
	groups   []GroupInfo
	keys     KeyMap
	manager  *service.Manager

	// UI state
	focus         FocusPanel
	cursor        int
	listScroll    int
	logScroll     int
	autoScroll    bool
	width         int
	height        int
	showHelp      bool
	viewingBuild  bool
	buildActive   bool
	buildLog      *service.RingBuffer
	quitting      bool
	hardQuit      bool
	confirmStop   string
	resourceStats map[string]msgs.ResourceStats
}

func NewModel(cfg *config.Config) Model {
	var services []*service.Service
	var groups []GroupInfo
	idx := 0

	for _, g := range cfg.Groups {
		gi := GroupInfo{
			Name:       g.Name,
			StartIndex: idx,
			Count:      len(g.Services),
		}
		groups = append(groups, gi)

		for _, sc := range g.Services {
			svc := service.NewService(sc.Name, g.Name, service.Kind(g.Kind))
			svc.Port = sc.Port
			svc.ComposeService = sc.ComposeService
			svc.ProjectPath = sc.Project
			svc.Directory = sc.Directory
			svc.InstallCommand = sc.InstallCommand
			svc.StartCommand = sc.StartCommand
			svc.DependsOn = sc.DependsOn
			svc.PreStartCmd = sc.PreStartCmd
			svc.PostStartCmd = sc.PostStartCmd
			services = append(services, svc)
			idx++
		}
	}

	mgr := service.NewManager(cfg.ProjectRoot, cfg.ComposeFile)
	mgr.RegisterServices(services)

	return Model{
		services:   services,
		groups:     groups,
		keys:       DefaultKeyMap(),
		manager:    mgr,
		autoScroll: true,
		buildLog:   service.NewRingBuffer(10000),
	}
}

// SetProgram passes the tea.Program to the manager for async message sending.
func (m Model) SetProgram(p *tea.Program) {
	m.manager.SetProgram(p)
}

// Manager returns the service manager so callers (e.g. main's signal handler)
// can drive shutdown independently of the bubbletea event loop.
func (m Model) Manager() *service.Manager {
	return m.manager
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		func() tea.Msg { return tea.RequestWindowSize() },
		m.manager.DetectRunning(m.services),
		m.manager.PollDockerStatus(m.services),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.adjustListScroll()
		return m, nil

	case tea.MouseClickMsg:
		mouse := msg.Mouse()
		if mouse.Button == tea.MouseLeft {
			lw := m.leftWidth()
			if mouse.X < lw {
				// Click in service list — map Y to service index.
				// Y=0 is the top border, content starts at Y=1.
				row := mouse.Y - 1 + m.listScroll
				if idx := m.serviceAtRow(row); idx >= 0 {
					m.viewingBuild = false
					m.cursor = idx
					m.focus = PanelServiceList
					m.adjustListScroll()
					m.autoScroll = true
					m.logScroll = -1
				}
			} else {
				m.focus = PanelLogs
			}
		}
		return m, nil

	case tea.MouseWheelMsg:
		mouse := msg.Mouse()
		if m.focus == PanelLogs {
			switch mouse.Button {
			case tea.MouseWheelUp:
				m.autoScroll = false
				m.logScroll -= 3
				if m.logScroll < 0 {
					m.logScroll = 0
				}
			case tea.MouseWheelDown:
				m.logScroll += 3
				m.clampLogScroll()
			}
		}
		return m, nil

	case tea.KeyPressMsg:
		if m.showHelp {
			if key.Matches(msg, m.keys.Help) || key.Matches(msg, m.keys.Quit) || key.Matches(msg, m.keys.Detach) || msg.String() == "esc" {
				m.showHelp = false
			}
			return m, nil
		}

		// Confirm dialog intercepts all keys
		if m.confirmStop != "" {
			switch msg.String() {
			case "y", "Y":
				svcName := m.confirmStop
				m.confirmStop = ""
				for _, svc := range m.services {
					if svc.Name == svcName {
						return m, m.manager.StopExternalProcess(svc)
					}
				}
			case "n", "N", "esc":
				m.confirmStop = ""
			}
			return m, nil
		}

		switch {
		case key.Matches(msg, m.keys.Detach):
			m.quitting = true
			m.manager.Detach()
			return m, func() tea.Msg { return tea.Quit() }

		case key.Matches(msg, m.keys.Quit):
			m.quitting = true
			m.hardQuit = true
			// Block until children are reaped before issuing Quit.
			// tea.Batch would race the two commands and Quit usually wins,
			// leaving services orphaned in their own process groups.
			mgr := m.manager
			return m, func() tea.Msg {
				mgr.StopAllProcesses()
				return tea.Quit()
			}

		case key.Matches(msg, m.keys.Help):
			m.showHelp = !m.showHelp
			return m, nil

		case key.Matches(msg, m.keys.Tab):
			if m.focus == PanelServiceList {
				m.focus = PanelLogs
			} else {
				m.focus = PanelServiceList
			}
			return m, nil

		case key.Matches(msg, m.keys.Up):
			if m.focus == PanelServiceList {
				m.viewingBuild = false
				m.cursor--
				if m.cursor < 0 {
					m.cursor = len(m.services) - 1
				}
				m.adjustListScroll()
				m.autoScroll = true
				m.logScroll = -1
			} else {
				m.autoScroll = false
				m.logScroll--
				if m.logScroll < 0 {
					m.logScroll = 0
				}
			}
			return m, nil

		case key.Matches(msg, m.keys.Down):
			if m.focus == PanelServiceList {
				m.viewingBuild = false
				m.cursor++
				if m.cursor >= len(m.services) {
					m.cursor = 0
				}
				m.adjustListScroll()
				m.autoScroll = true
				m.logScroll = -1
			} else {
				m.autoScroll = false
				m.logScroll++
			}
			return m, nil

		case key.Matches(msg, m.keys.PageUp):
			if m.focus == PanelLogs {
				m.autoScroll = false
				m.logScroll -= m.logContentHeight()
				if m.logScroll < 0 {
					m.logScroll = 0
				}
			}
			return m, nil

		case key.Matches(msg, m.keys.PageDown):
			if m.focus == PanelLogs {
				m.autoScroll = false
				m.logScroll += m.logContentHeight()
				m.clampLogScroll()
			}
			return m, nil

		case key.Matches(msg, m.keys.Home):
			if m.focus == PanelLogs {
				m.autoScroll = false
				m.logScroll = 0
			}
			return m, nil

		case key.Matches(msg, m.keys.End):
			if m.focus == PanelLogs {
				m.autoScroll = true
				m.logScroll = -1
			}
			return m, nil

		case key.Matches(msg, m.keys.Toggle):
			if m.focus == PanelServiceList && m.cursor >= 0 && m.cursor < len(m.services) {
				svc := m.services[m.cursor]
				if svc.State == service.StateExternal {
					m.confirmStop = svc.Name
					return m, nil
				}
				m.viewingBuild = false
				return m, m.manager.Toggle(svc)
			}
			return m, nil

		case key.Matches(msg, m.keys.StartGroup):
			if m.focus == PanelServiceList {
				if g := m.findCurrentGroup(); g != nil {
					return m, m.manager.StartGroup(m.services, g.StartIndex, g.Count)
				}
			}
			return m, nil

		case key.Matches(msg, m.keys.StopGroup):
			if m.focus == PanelServiceList {
				if g := m.findCurrentGroup(); g != nil {
					return m, m.manager.StopGroup(m.services, g.StartIndex, g.Count)
				}
			}
			return m, nil

		case key.Matches(msg, m.keys.StartAll):
			return m, m.manager.StartAll(m.services)

		case key.Matches(msg, m.keys.StopAll):
			return m, m.manager.StopAll(m.services)

		case key.Matches(msg, m.keys.Rebuild):
			if m.focus == PanelServiceList && m.cursor >= 0 && m.cursor < len(m.services) {
				m.viewingBuild = false
				return m, m.manager.Rebuild(m.services[m.cursor])
			}
			return m, nil

		case key.Matches(msg, m.keys.Build):
			if m.buildActive {
				m.viewingBuild = true
				m.autoScroll = true
				m.logScroll = -1
				return m, nil
			}
			m.buildLog.Clear()
			m.buildActive = true
			m.viewingBuild = true
			m.autoScroll = true
			m.logScroll = -1
			return m, m.manager.Build()

		case key.Matches(msg, m.keys.StopDocker):
			return m, m.manager.StopDocker(m.services)

		case key.Matches(msg, m.keys.Follow):
			if m.focus == PanelLogs {
				m.autoScroll = !m.autoScroll
				if m.autoScroll {
					m.logScroll = -1
				}
			}
			return m, nil

		case key.Matches(msg, m.keys.OpenPager):
			return m, m.openInPager()
		}

	case msgs.StateChanged:
		for _, svc := range m.services {
			if svc.Name == msg.ServiceName {
				svc.State = service.State(msg.NewState)
				svc.Error = msg.Error
				break
			}
		}
		return m, nil

	case msgs.LogLine:
		// Registered services write directly to their buffer via the
		// manager's fast path; LogLine now only carries unregistered
		// names like "[build]".
		if msg.ServiceName == "[build]" {
			m.buildLog.Write(msg.Line)
		}
		return m, nil

	case msgs.LogsUpdated:
		// Coalesced wake-up — buffers are already written. Returning
		// triggers a re-render against the current buffer state.
		return m, nil

	case msgs.ProcessExited:
		for _, svc := range m.services {
			if svc.Name == msg.ServiceName {
				if msg.Error != nil {
					svc.State = service.StateError
					svc.Error = msg.Error
				} else {
					svc.State = service.StateStopped
				}
				break
			}
		}
		return m, nil

	case msgs.BuildDone:
		m.buildActive = false
		return m, nil

	case msgs.ExternalServiceDetected:
		for _, svc := range m.services {
			if svc.Name == msg.ServiceName {
				svc.State = service.StateExternal
				svc.ExternalPID = msg.PID
				break
			}
		}
		return m, nil

	case msgs.ResourceUpdate:
		m.resourceStats = msg.Stats
		return m, nil

	case msgs.TickMsg:
		return m, m.manager.PollDockerStatus(m.services)
	}

	return m, nil
}

func (m Model) View() tea.View {
	var v tea.View
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion

	var content string
	if m.quitting {
		if m.hardQuit {
			content = "Stopping services...\n"
		} else {
			content = "Detaching (services still running)...\n"
		}
	} else if m.width == 0 || m.height == 0 {
		content = "Loading..."
	} else if m.showHelp {
		content = m.renderHelp()
	} else {
		lw := m.leftWidth()
		rightWidth := m.width - lw
		mainHeight := m.height - 1

		rph := m.resPanelHeight()
		serviceListHeight := mainHeight - rph

		left := renderServiceList(m.services, m.groups, m.cursor, m.listScroll, lw, serviceListHeight, m.focus == PanelServiceList)

		if rph > 0 {
			resPanel := renderResourcePanel(m.resourceStats, lw, rph)
			left = lipgloss.JoinVertical(lipgloss.Left, left, resPanel)
		}

		logTitle, logBuffer := m.currentLogInfo()
		right := renderLogViewer(logTitle, logBuffer, rightWidth, mainHeight, m.focus == PanelLogs, m.logScroll, m.autoScroll)

		main := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
		statusBar := renderStatusBar(m.services, m.keys, m.width, m.focus, m.confirmStop)

		output := lipgloss.JoinVertical(lipgloss.Left, main, statusBar)

		// Clamp to terminal height — if rendering produces even one extra line,
		// the terminal scrolls the alt screen buffer.
		lines := strings.Split(output, "\n")
		if len(lines) > m.height {
			lines = lines[:m.height]
		}
		content = strings.Join(lines, "\n")
	}

	v.SetContent(content)
	return v
}

// serviceAtRow maps a content-row index (in the flat list of group headers +
// services) to a service index. Returns -1 for group headers or out-of-range.
func (m *Model) serviceAtRow(row int) int {
	line := 0
	for _, g := range m.groups {
		if row == line {
			return -1 // group header
		}
		line++ // skip header
		if !g.Collapsed {
			for i := 0; i < g.Count; i++ {
				if row == line {
					return g.StartIndex + i
				}
				line++
			}
		}
	}
	return -1
}

// leftWidth returns the width of the service list panel.
func (m *Model) leftWidth() int {
	w := m.width * 30 / 100
	if w < 30 {
		w = 30
	}
	if w > 50 {
		w = 50
	}
	return w
}

// contentHeight returns the visible line count inside a panel (total height
// minus status bar and borders).
func (m *Model) contentHeight() int {
	h := m.height - 1 - 2 // status bar, borders
	if h < 1 {
		h = 1
	}
	return h
}

// logContentHeight returns the visible line count of the log panel.
func (m *Model) logContentHeight() int {
	return m.contentHeight()
}

func (m *Model) clampLogScroll() {
	_, buf := m.currentLogInfo()
	if buf == nil {
		return
	}

	contentWidth := (m.width - m.leftWidth()) - 4
	if contentWidth < 10 {
		contentWidth = 10
	}

	// Count wrapped lines
	rawLines := buf.Lines()
	totalLines := 0
	for _, line := range rawLines {
		wrapped := ansi.Hardwrap(line, contentWidth, false)
		totalLines += strings.Count(wrapped, "\n") + 1
	}

	maxScroll := totalLines - m.contentHeight()
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.logScroll > maxScroll {
		m.logScroll = maxScroll
	}
}

// resPanelHeight returns the total height (including borders) of the resource
// panel, or 0 when it should be hidden.
func (m *Model) resPanelHeight() int {
	n := len(m.resourceStats)
	if n == 0 {
		return 0
	}
	h := resourcePanelContentHeight(n) + resourcePanelBorders
	mainHeight := m.height - 1
	if mainHeight-h < 10 { // keep at least 10 lines for the service list
		return 0
	}
	return h
}

func (m *Model) serviceListContentHeight() int {
	h := m.height - 1 - m.resPanelHeight() - 2 // status bar, resource panel, borders
	if h < 1 {
		h = 1
	}
	return h
}

func (m *Model) adjustListScroll() {
	ch := m.serviceListContentHeight()
	cursorLine := findCursorLine(m.groups, m.cursor)

	// Total lines including group headers
	totalLines := 0
	for _, g := range m.groups {
		totalLines++ // header
		if !g.Collapsed {
			totalLines += g.Count
		}
	}

	if totalLines <= ch {
		m.listScroll = 0
		return
	}
	if cursorLine >= m.listScroll+ch {
		m.listScroll = cursorLine - ch + 1
	}
	if cursorLine < m.listScroll {
		m.listScroll = cursorLine
	}
	maxScroll := totalLines - ch
	if m.listScroll > maxScroll {
		m.listScroll = maxScroll
	}
	if m.listScroll < 0 {
		m.listScroll = 0
	}
}

func (m Model) findCurrentGroup() *GroupInfo {
	for i := range m.groups {
		g := &m.groups[i]
		if m.cursor >= g.StartIndex && m.cursor < g.StartIndex+g.Count {
			return g
		}
	}
	return nil
}

// currentLogInfo returns the title and log buffer for the active log view.
func (m *Model) currentLogInfo() (string, *service.RingBuffer) {
	if m.viewingBuild {
		return "Build", m.buildLog
	}
	if m.cursor >= 0 && m.cursor < len(m.services) {
		svc := m.services[m.cursor]
		return svc.Name, svc.LogBuffer
	}
	return "", nil
}

// openInPager writes the service's log buffer to a temp file and opens it
// in $PAGER (or less). Bubble Tea suspends the TUI, gives full terminal
// control to the pager, and resumes when the user quits.
func (m Model) openInPager() tea.Cmd {
	_, buf := m.currentLogInfo()
	if buf == nil {
		return nil
	}

	lines := buf.Lines()
	if len(lines) == 0 {
		return nil
	}

	content := strings.Join(lines, "\n") + "\n"

	return func() tea.Msg {
		tmpFile, err := os.CreateTemp("", "devtui-logs-*.log")
		if err != nil {
			return nil
		}
		tmpPath := tmpFile.Name()
		tmpFile.WriteString(content)
		tmpFile.Close()

		pager := os.Getenv("PAGER")
		if pager == "" {
			pager = "less"
		}

		// less -R preserves ANSI colors, +G starts at the bottom
		var cmd *exec.Cmd
		if pager == "less" {
			cmd = exec.Command(pager, "-R", "+G", tmpPath)
		} else {
			cmd = exec.Command(pager, tmpPath)
		}

		return tea.ExecProcess(cmd, func(err error) tea.Msg {
			os.Remove(tmpPath)
			return nil
		})()
	}
}

func (m Model) renderHelp() string {
	help := lipgloss.NewStyle().Padding(2, 4).Render(
		lipgloss.JoinVertical(lipgloss.Left,
			lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86")).Render("devtui — Key Bindings"),
			"",
			formatHelpLine("↑/k, ↓/j", "Navigate services / scroll logs"),
			formatHelpLine("Enter/Space", "Start/stop selected service"),
			formatHelpLine("Tab", "Switch between panels"),
			formatHelpLine("Click", "Select service"),
			formatHelpLine("g", "Start all in current group"),
			formatHelpLine("G", "Stop all in current group"),
			formatHelpLine("a", "Start all services"),
			formatHelpLine("A", "Stop all non-docker services"),
			formatHelpLine("r", "Rebuild selected service"),
			formatHelpLine("b", "Build dotnet solution"),
			formatHelpLine("D", "Stop all Docker services"),
			formatHelpLine("PgUp/PgDn", "Scroll logs by page"),
			formatHelpLine("Home/End", "Scroll to top/bottom"),
			formatHelpLine("f", "Toggle log auto-scroll (follow)"),
			formatHelpLine("o", "Open logs in pager (copy/search)"),
			formatHelpLine("?", "Toggle this help"),
			formatHelpLine("q", "Detach (leave services running)"),
			formatHelpLine("Q / Ctrl+C", "Quit (stop all services)"),
			"",
			HelpDescStyle.Render("Press ? or Esc to close"),
		),
	)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, help)
}

func formatHelpLine(keyStr, desc string) string {
	return HelpKeyStyle.Width(16).Render(keyStr) + HelpDescStyle.Render(desc)
}
