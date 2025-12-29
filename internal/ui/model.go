package ui

import (
	"fmt"
	"strings"
	"time"

	"adblock/internal/core"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Styles
var (
	subtle    = lipgloss.AdaptiveColor{Light: "#D9DCCF", Dark: "#383838"}
	highlight = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
	special   = lipgloss.AdaptiveColor{Light: "#43BF6D", Dark: "#73F59F"}

	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(highlight).
			Padding(0, 1).
			Bold(true)

	statusStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1).
			BorderForeground(subtle)

	logStyle = lipgloss.NewStyle().
			Foreground(subtle)
)

type tickMsg time.Time
type logMsg string

type Model struct {
	svc core.Service

	// Stats
	startTime      time.Time
	queriesTotal   int
	queriesBlocked int

	// Logs
	logLines []string
	
	// View State
	activeTab  int
	menuFocus  bool // True if user is navigating the top menu
	menuCursor int  // Which tab is highlighted in the menu
	listCursor int  // Which item is highlighted in the list content

	isLoading bool // True while blocklists are initializing

	width  int
	height int
}

func NewModel(svc core.Service) Model {
	return Model{
		svc:        svc,
		startTime:  time.Now(),
		logLines:   []string{"System Initialized...", "Connecting to Service..."},
		activeTab:  0,
		menuCursor: 0,
		isLoading:  true,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyTab:
			m.menuFocus = !m.menuFocus
			if m.menuFocus {
				m.menuCursor = m.activeTab // Sync cursor when entering menu
			}

		case tea.KeyLeft:
			if m.menuFocus {
				m.menuCursor = 0
			}
		case tea.KeyRight:
			if m.menuFocus {
				m.menuCursor = 1
			}

		case tea.KeyEnter, tea.KeySpace:
			if m.menuFocus {
				m.activeTab = m.menuCursor
				m.menuFocus = false // Exit menu on selection
			} else if m.activeTab == 1 {
				m.toggleCurrentSource()
			}

		case tea.KeyUp:
			if !m.menuFocus && m.activeTab == 1 && m.listCursor > 0 {
				m.listCursor--
			}
		case tea.KeyDown:
			srcs, _ := m.svc.ListSources() // Error ignored for navigation check
			if !m.menuFocus && m.activeTab == 1 && m.listCursor < len(srcs)-1 {
				m.listCursor++
			}

		case tea.KeyRunes:
			// Shortcuts that work when NOT in menu
			if !m.menuFocus {
				switch string(msg.Runes) {
				case "q":
					return m, tea.Quit
				case "k":
					if m.activeTab == 1 && m.listCursor > 0 {
						m.listCursor--
					}
				case "j":
					srcs, _ := m.svc.ListSources()
					if m.activeTab == 1 && m.listCursor < len(srcs)-1 {
						m.listCursor++
					}
				case " ":
					if m.activeTab == 1 {
						m.toggleCurrentSource()
					}
				case "r":
					// Allow async reload trigger
					go m.svc.Reload()
				}
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeContent(msg.Width)

	case tickMsg:
		// POLL STATS
		var activeRules int
		var err error
		m.queriesTotal, m.queriesBlocked, activeRules, err = m.svc.GetStats()
		if err != nil {
			// If service is down/unreachable
			m.logLines = append(m.logLines, fmt.Sprintf("Error fetching stats: %v", err))
		}
		
		// Fallback: If we have active rules, we are loaded.
		if m.isLoading && activeRules > 0 {
			m.isLoading = false
		}

		// POLL LOGS
		// Get last 50 logs directly from service (State held in service now)
		newLogs, err := m.svc.GetRecentLogs(50)
		if err == nil {
			m.logLines = newLogs
			
			// Check for "blocked" messages in logs to detect loading completion
			// (Optimization: can rely on activeRules above, but keeping for robustness)
			for _, l := range newLogs {
				if strings.Contains(l, "Total Unique Rules") {
					m.isLoading = false
				}
			}
		}

		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg {
			return tickMsg(t)
		})
	}

	return m, nil
}

func (m *Model) toggleCurrentSource() {
	sources, _ := m.svc.ListSources()
	if len(sources) > 0 {
		src := sources[m.listCursor]
		err := m.svc.ToggleSource(src.Name, !src.Enabled)
		if err != nil {
			// In poll mode, we can't push to log immediately easily unless we have a local log function.
			// Ideally service.ToggleSource should log internally.
		}
	}
}

func (m *Model) resizeContent(width int) {
	statusStyle = statusStyle.Width(width/2 - 2)
}

func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	// Header
	header := headerStyle.Width(m.width).Render("GO-SINKHOLE PROTECTION SYSTEM")

	// Styles for Tabs (Hex Colors)
	activeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color("#874BFD")). // Violet
		Bold(true).
		Padding(0, 1)

	inactiveStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#808080")).
		Background(lipgloss.Color("#303030")).
		Padding(0, 1)

	focusStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color("#43BF6D")). // Green
		Padding(0, 1)

	// Debug Trace
	// tea.Log("View Render:", "activeTab", m.activeTab, "menuFocus", m.menuFocus, "menuCursor", m.menuCursor)
	debugStr := fmt.Sprintf("DEBUG: ActiveTab=%d MenuFocus=%v MenuCursor=%d Width=%d", m.activeTab, m.menuFocus, m.menuCursor, m.width)
	_ = debugStr // Use it or render it

	var dashTab, listTab string

	// Dashboard Logic

	if m.menuFocus {
		if m.menuCursor == 0 {
			dashTab = focusStyle.Render("DASHBOARD")
		} else {
			dashTab = inactiveStyle.Render("DASHBOARD")
		}

		if m.menuCursor == 1 {
			listTab = focusStyle.Render("LISTS")
		} else {
			listTab = inactiveStyle.Render("LISTS")
		}
	} else {
		if m.activeTab == 0 {
			dashTab = activeStyle.Render("DASHBOARD")
		} else {
			dashTab = inactiveStyle.Render("DASHBOARD")
		}

		if m.activeTab == 1 {
			listTab = activeStyle.Render("LISTS")
		} else {
			listTab = inactiveStyle.Render("LISTS")
		}
	}

	// Simple row construction
	tabStr := lipgloss.JoinHorizontal(lipgloss.Top, dashTab, "  ", listTab)

	// Calc Available Height
	// Overhead: Header(1) + Debug(1) + Tabs(3 w/ border) + Spacers(4) + DashStatus(6) + LogHeader(2) = ~17 lines
	fixedHeight := 19
	logHeight := m.height - fixedHeight
	if logHeight < 5 {
		logHeight = 5 // Min height
	}

	content := ""

	if m.activeTab == 0 {
		// --- DASHBOARD VIEW ---
		uptime := time.Since(m.startTime).Round(time.Second)
		_, _, blockedCount, _ := m.svc.GetStats()
		srcs, _ := m.svc.ListSources()

		status := "Running"
		if m.isLoading {
			status = "LOADING..."
		}

		stats := fmt.Sprintf(
			"STATUS:  %s\nUPTIME:  %s\nBLOCKED: %d (%d%%)\nTOTAL:   %d",
			status,
			uptime,
			m.queriesBlocked,
			opts(m.queriesTotal, m.queriesBlocked),
			m.queriesTotal,
		)

		statsBox := statusStyle.
			Height(6).
			Width(m.width/2 - 2).
			Render(stats)

		blStatus := fmt.Sprintf("Active Rules: %d\nSources:      %d", blockedCount, len(srcs))
		blBox := statusStyle.
			Height(6).
			Width(m.width/2 - 2).
			Render(blStatus)

		headerBlock := lipgloss.JoinHorizontal(lipgloss.Top, statsBox, blBox)

		// Log Tail
		linesToShow := logHeight
		start := 0
		if len(m.logLines) > linesToShow {
			start = len(m.logLines) - linesToShow
		}
		visibleLogs := m.logLines[start:]

		logBox := logStyle.
			Height(logHeight).
			Width(m.width - 2).
			Render(strings.Join(visibleLogs, "\n"))

		content = lipgloss.JoinVertical(lipgloss.Left, headerBlock, "\nLOGS:", logBox)

	} else {
		// --- LIST MANAGEMENT VIEW ---
		sources, _ := m.svc.ListSources()

		// Viewport logic
		startRow := 0
		if m.listCursor >= logHeight {
			startRow = m.listCursor - logHeight + 1
		}
		endRow := startRow + logHeight
		if endRow > len(sources) {
			endRow = len(sources)
		}

		var listContent []string
		listContent = append(listContent, "  [SPACE] Toggle  [R] Reload/Apply\n")

		for i := startRow; i < endRow; i++ {
			src := sources[i]
			cursor := "  "
			if m.listCursor == i {
				cursor = "> "
			}
			checked := "[ ]"
			if src.Enabled {
				checked = "[x]"
			}
			line := fmt.Sprintf("%s%s %s (%s)", cursor, checked, src.Name, src.Format)
			if m.listCursor == i {
				line = headerStyle.Render(line)
			}
			listContent = append(listContent, line)
		}
		content = strings.Join(listContent, "\n")
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, "\n", tabStr, "\n", content)
}

func opts(total, blocked int) int {
	if total == 0 {
		return 0
	}
	return (blocked * 100) / total
}
