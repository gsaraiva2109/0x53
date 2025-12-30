package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"0x53/internal/core"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Styles
var (
	subtle    = lipgloss.AdaptiveColor{Light: "#D9DCCF", Dark: "#383838"}
	highlight = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}

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

	// Table Styles
	baseTableStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("240"))
)

type tickMsg time.Time

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

	// Input State (Legacy for Allowlist)
	inputMode bool
	inputText string

	// Local Records Table & Form
	localTable table.Model
	inputs     []textinput.Model
	focusIndex int
	showForm   bool

	width  int
	height int
}

func NewModel(svc core.Service) Model {
	// Initialize Table
	columns := []table.Column{
		{Title: "IP Address", Width: 20},
		{Title: "Domain", Width: 40},
	}
	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(true),
		table.WithHeight(10),
	)
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(false)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(false)
	t.SetStyles(s)

	// Initialize Inputs (0: IP, 1: Domain)
	inputs := make([]textinput.Model, 2)
	inputs[0] = textinput.New()
	inputs[0].Placeholder = "192.168.1.1"
	inputs[0].CharLimit = 15
	inputs[0].Width = 20
	inputs[0].Prompt = "IP: "

	inputs[1] = textinput.New()
	inputs[1].Placeholder = "router.lan"
	inputs[1].CharLimit = 50
	inputs[1].Width = 40
	inputs[1].Prompt = "Domain: "

	return Model{
		svc:        svc,
		startTime:  time.Now(),
		logLines:   []string{"System Initialized...", "Connecting to Service..."},
		activeTab:  0,
		menuCursor: 0,
		isLoading:  true,
		localTable: t,
		inputs:     inputs,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	// Handle Form Input if active
	if m.showForm {
		return m.updateForm(msg)
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Global Shortcuts
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q":
			if !m.inputMode && !m.showForm {
				return m, tea.Quit
			}
		case "r":
			if !m.inputMode && !m.showForm {
				go m.svc.Reload()
				m.logLines = append(m.logLines, "Reload triggered...")
			}
		}

		// Navigation Logic
		switch msg.Type {
		case tea.KeyTab:
			m.menuFocus = !m.menuFocus
			if m.menuFocus {
				m.menuCursor = m.activeTab
			}

		case tea.KeyLeft:
			if m.menuFocus {
				m.menuCursor = max(0, m.menuCursor-1)
			}
		case tea.KeyRight:
			if m.menuFocus {
				m.menuCursor = min(3, m.menuCursor+1)
			}

		case tea.KeyEnter:
			if m.menuFocus {
				m.activeTab = m.menuCursor
				m.menuFocus = false
				m.listCursor = 0
				// Refresh Table on tab switch
				if m.activeTab == 3 {
					m.refreshTable()
				}
			} else if m.inputMode {
				// Legacy Allowlist Input
				if m.inputText != "" {
					if m.activeTab == 2 {
						m.svc.AddAllowed(m.inputText)
					}
				}
				m.inputMode = false
				m.inputText = ""
			} else if m.activeTab == 1 {
				m.toggleCurrentSource()
			}

		case tea.KeySpace:
			if !m.inputMode && !m.menuFocus {
				if m.activeTab == 1 {
					m.toggleCurrentSource()
				}
			} else if m.inputMode {
				m.inputText += " "
			}

		case tea.KeyBackspace, tea.KeyDelete:
			if m.inputMode && len(m.inputText) > 0 {
				m.inputText = m.inputText[:len(m.inputText)-1]
			}

		case tea.KeyRunes:
			if m.inputMode {
				m.inputText += msg.String()
			}

		// List Navigation (Manual for standard lists, Table handles its own)
		case tea.KeyUp, tea.KeyDown:
			if !m.menuFocus && !m.inputMode && m.activeTab != 3 {
				// ... (Keep existing simple list nav logic) ...
				if msg.Type == tea.KeyUp && m.listCursor > 0 {
					m.listCursor--
				}
				if msg.Type == tea.KeyDown {
					// Naive upper bound check isn't strictly needed if View handles clamping,
					// but it's good UX to stop cursor.
					// We need to know the limit.
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
			}
		}

		// Shortcuts
		if !m.inputMode && !m.menuFocus {
			switch msg.String() {
			case "k": // UP
				if m.activeTab != 3 && m.listCursor > 0 {
					m.listCursor--
				}
			case "j": // DOWN
				if m.activeTab != 3 {
					m.listCursor++
				} // Need limit check ideally

			// Actions
			case "a":
				if m.activeTab == 2 {
					m.inputMode = true
					m.inputText = ""
				} else if m.activeTab == 3 {
					m.showForm = true
					m.focusIndex = 0
					m.inputs[0].Focus()
					return m, textinput.Blink
				}
			case "d":
				if m.activeTab == 2 {
					// Delete Allowlist
					list, _ := m.svc.ListAllowed()
					if m.listCursor < len(list) {
						m.svc.RemoveAllowed(list[m.listCursor])
					}
				} else if m.activeTab == 3 {
					// Delete Local Record
					sel := m.localTable.SelectedRow()
					if len(sel) > 1 {
						// sel[1] is Domain because Columns are IP, Domain
						m.svc.RemoveLocalRecord(sel[1])
						m.refreshTable()
					}
				}
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeContent(msg.Width)

	case tickMsg:
		// ... (Keep Stats/Log Poll logic) ...
		var activeRules int
		var err error
		m.queriesTotal, m.queriesBlocked, activeRules, err = m.svc.GetStats()
		if err != nil {
			// If service is down/unreachable
			m.logLines = append(m.logLines, fmt.Sprintf("Error fetching stats: %v", err))
		}
		if err == nil {
			if m.isLoading && activeRules > 0 {
				m.isLoading = false
			}
		}
		newLogs, err := m.svc.GetRecentLogs(50)
		if err == nil {
			m.logLines = newLogs
		}

		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
	}

	// Update Table if visible
	if m.activeTab == 3 && !m.menuFocus {
		m.localTable, cmd = m.localTable.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m Model) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			if m.focusIndex == len(m.inputs)-1 {
				// Submit
				ip := m.inputs[0].Value()
				domain := m.inputs[1].Value()
				if ip != "" && domain != "" {
					m.svc.AddLocalRecord(domain, ip)
					m.refreshTable()
				}
				// Reset
				m.inputs[0].SetValue("")
				m.inputs[1].SetValue("")
				m.showForm = false
				return m, nil
			}
			m.focusIndex++
		case "tab", "shift+tab":
			if msg.String() == "tab" {
				m.focusIndex++
			} else {
				m.focusIndex--
			}
			if m.focusIndex > len(m.inputs)-1 {
				m.focusIndex = 0
			}
			if m.focusIndex < 0 {
				m.focusIndex = len(m.inputs) - 1
			}
		case "esc":
			m.showForm = false
			return m, nil
		}
	}

	// Update inputs
	cmds := make([]tea.Cmd, len(m.inputs))
	for i := range m.inputs {
		if i == m.focusIndex {
			cmds[i] = m.inputs[i].Focus()
			m.inputs[i], cmds[i] = m.inputs[i].Update(msg)
		} else {
			m.inputs[i].Blur()
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) refreshTable() {
	records, _ := m.svc.ListLocalRecords()
	// Sort by IP for display
	keys := make([]string, 0, len(records))
	for k := range records {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	rows := []table.Row{}
	for _, domain := range keys {
		ip := records[domain]
		// Columns: IP, Domain
		rows = append(rows, table.Row{ip, domain})
	}
	m.localTable.SetRows(rows)
}

func (m *Model) toggleCurrentSource() {
	sources, _ := m.svc.ListSources()
	if len(sources) > 0 && m.listCursor < len(sources) {
		src := sources[m.listCursor]
		m.svc.ToggleSource(src.Name, !src.Enabled)
	}
}

func (m *Model) resizeContent(width int) {
	statusStyle = statusStyle.Width(width/2 - 2)
	// Resize Table
	m.localTable.SetWidth(width - 4)
	m.localTable.SetHeight(m.height - 20) // Heuristic
}

func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	// Header
	header := headerStyle.Width(m.width).Render("0x53 PROTECTION SYSTEM")

	// Tabs logic
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

	tabs := []string{"DASHBOARD", "LISTS", "ALLOW", "LOCAL"}
	renderedTabs := make([]string, len(tabs))

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
	tabStr := lipgloss.JoinHorizontal(lipgloss.Top, renderedTabs...)

	// ... (Rest of resizing logic) ...
	fixedHeight := 19
	logHeight := m.height - fixedHeight
	if logHeight < 5 {
		logHeight = 5 // Min height
	}

	content := ""

	if m.showForm {
		// Form View
		content = fmt.Sprintf(
			"Add Local Record:\n\n%s\n\n%s\n\n[ENTER] Next/Submit  [ESC] Cancel",
			m.inputs[0].View(),
			m.inputs[1].View(),
		)
		// Center it a bit
		content = lipgloss.Place(m.width, m.height-5, lipgloss.Center, lipgloss.Center, content)
	} else if m.activeTab == 0 {
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

	} else if m.activeTab == 1 {
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

	} else if m.activeTab == 2 {
		// --- ALLOWLIST VIEW ---
		allowlist, _ := m.svc.ListAllowed() // Should ideally sort this list

		if m.inputMode {
			content = fmt.Sprintf("Add Domain to Allowlist:\n\n> %s_", m.inputText)
			content += "\n\n[ENTER] Save   [ESC] Cancel"
		} else {
			header := "  [A] Add Domain  [D] Delete Selected\n"

			// Viewport logic
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

			// Viewport Calc
			endRow := startRow + logHeight
			if endRow > len(allowlist) {
				endRow = len(allowlist)
			}

			// Render
			var listRows []string
			listRows = append(listRows, header)

			if len(allowlist) == 0 {
				listRows = append(listRows, "\n  (No allowed domains)")
			}

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
	} else if m.activeTab == 3 {
		// Local Table
		content = baseTableStyle.Render(m.localTable.View())
		content += "\n  [A] Add Record  [D] Delete  [R] Soft Reload"
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, "\n", tabStr, "\n", content)
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
func opts(total, blocked int) int {
	if total == 0 {
		return 0
	}
	return (blocked * 100) / total
}
