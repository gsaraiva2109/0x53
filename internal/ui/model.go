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
	
	// Input State
	inputMode bool
	inputText string

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
				m.menuCursor--
				if m.menuCursor < 0 {
					m.menuCursor = 0
				}
			}
	case tea.KeyRight:
			if m.menuFocus {
				m.menuCursor++
				if m.menuCursor > 2 {
					m.menuCursor = 2
				}
			}

		case tea.KeyEnter:
			if m.menuFocus {
				m.activeTab = m.menuCursor
				m.menuFocus = false 
				m.listCursor = 0 // Reset list cursor when switching tabs
			} else if m.inputMode {
				// Save Domain
				if m.inputText != "" {
					m.svc.AddAllowed(m.inputText)
				}
				m.inputMode = false
				m.inputText = ""
			} else if m.activeTab == 1 {
				m.toggleCurrentSource()
			}

		case tea.KeySpace:
			if !m.inputMode {
				if m.menuFocus {
					m.activeTab = m.menuCursor
					m.menuFocus = false 
				} else if m.activeTab == 1 {
					m.toggleCurrentSource()
				}
			} else {
				m.inputText += " "
			}

		case tea.KeyBackspace, tea.KeyDelete:
			if m.inputMode && len(m.inputText) > 0 {
				m.inputText = m.inputText[:len(m.inputText)-1]
			}

		case tea.KeyUp:
			if !m.menuFocus && !m.inputMode {
				if m.listCursor > 0 {
					m.listCursor--
				}
			}
		case tea.KeyDown:
			if !m.menuFocus && !m.inputMode {
				// Naive bounds check for different tabs
				limit := 0
				if m.activeTab == 1 {
					srcs, _ := m.svc.ListSources()
					limit = len(srcs)
				} else if m.activeTab == 2 {
					list, _ := m.svc.ListAllowed()
					limit = len(list)
				}
				
				if m.listCursor < limit-1 {
					m.listCursor++
				}
			}

		case tea.KeyRunes:
			// Shortcuts that work when NOT in menu
			if m.inputMode {
				m.inputText += string(msg.Runes)
			} else if !m.menuFocus {
				switch string(msg.Runes) {
				case "q":
					return m, tea.Quit
				case "k": // Vim Up
					if m.listCursor > 0 {
						m.listCursor--
					}
				case "j": // Vim Down
					limit := 0
					if m.activeTab == 1 {
						srcs, _ := m.svc.ListSources()
						limit = len(srcs)
					} else if m.activeTab == 2 {
						list, _ := m.svc.ListAllowed()
						limit = len(list)
					}
					if m.listCursor < limit-1 {
						m.listCursor++
					}
				case " ":
					if m.activeTab == 1 {
						m.toggleCurrentSource()
					}
				case "r":
					go m.svc.Reload()
				case "a":
					if m.activeTab == 2 {
						m.inputMode = true
						m.inputText = ""
					}
				case "d":
					if m.activeTab == 2 {
						// Delete selected
						list, _ := m.svc.ListAllowed()
						if m.listCursor < len(list) {
							m.svc.RemoveAllowed(list[m.listCursor])
						}
					}
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

	// --- NAVIGATION ---
	var dashTab, listTab, allowTab string
	
	tabs := []string{"DASHBOARD", "LISTS", "ALLOW"}
	renderedTabs := make([]string, 3)

	for i, t := range tabs {
		style := inactiveStyle
		if m.menuFocus {
			if m.menuCursor == i {
				style = focusStyle
			}
		} else {
			if m.activeTab == i {
				style = activeStyle
			}
		}
		renderedTabs[i] = style.Render(t)
	}
	
	dashTab, listTab, allowTab = renderedTabs[0], renderedTabs[1], renderedTabs[2]
	tabStr := lipgloss.JoinHorizontal(lipgloss.Top, dashTab, " ", listTab, " ", allowTab)

	// ... (Rest of resizing logic) ...
	fixedHeight := 19
	logHeight := m.height - fixedHeight
	if logHeight < 5 {
		logHeight = 5 // Min height
	}

	content := ""

	if m.activeTab == 0 {
		// --- DASHBOARD VIEW --- 
		// (Keep existing Dashboard logic)
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

	} else if m.activeTab == 1 {
		// --- LIST MANAGEMENT VIEW ---
		// (Keep existing Lists logic)
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
	} else if m.activeTab == 2 {
		// --- ALLOWLIST VIEW ---
		allowlist, _ := m.svc.ListAllowed()
		
		if m.inputMode {
			content = fmt.Sprintf("Add Domain to Allowlist:\n\n> %s_", m.inputText)
			content += "\n\n[ENTER] Save   [ESC] Cancel"
		} else {
			header := "  [A] Add Domain  [D] Delete Selected\n"
			
			// Viewport logic (reuse simple logic for now)
			// Ensure cursor is valid
			if m.listCursor >= len(allowlist) {
				m.listCursor = len(allowlist) - 1
			}
			if m.listCursor < 0 {
				m.listCursor = 0
			}

			startRow := 0
			if m.listCursor >= logHeight {
				startRow = m.listCursor - logHeight + 1
			}
			// Simple pagination if list is long
			// ...
			
			var listRows []string
			listRows = append(listRows, header)
			
			if len(allowlist) == 0 {
				listRows = append(listRows, "\n  (No allowed domains)")
			}

			// Viewport Calc
			endRow := startRow + logHeight
			if endRow > len(allowlist) {
				endRow = len(allowlist)
			}

			// Render
			for i := startRow; i < endRow; i++ {
				domain := allowlist[i]
				cursor := "  "
				if m.listCursor == i {
					cursor = "> "
				}
				line := fmt.Sprintf("%s%s", cursor, domain)
				if m.listCursor == i {
					line = headerStyle.Render(line)
				}
				listRows = append(listRows, line)
			}
			content = strings.Join(listRows, "\n")
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, "\n", tabStr, "\n", content)
}

func opts(total, blocked int) int {
	if total == 0 {
		return 0
	}
	return (blocked * 100) / total
}
