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

// Package-level styles. Tab styles are here too so they are not
// reallocated on every View() call.
var (
	subtle    = lipgloss.AdaptiveColor{Light: "#D9DCCF", Dark: "#383838"}
	highlight = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}

	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(highlight).
			Padding(0, 1).
			Bold(true)

	// statusStyle base — width is applied locally in View() to avoid mutating global state.
	statusStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1).
			BorderForeground(subtle)

	logStyle = lipgloss.NewStyle().
			Foreground(subtle)

	baseTableStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("240"))

	tabActive = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#874BFD")).
			Bold(true).
			Padding(0, 1)

	tabInactive = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#808080")).
			Background(lipgloss.Color("#303030")).
			Padding(0, 1)

	tabFocus = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#43BF6D")).
			Padding(0, 1)
)

type tickMsg time.Time
type reloadDoneMsg struct{ err error }

type Model struct {
	svc core.Service

	// Stats
	startTime      time.Time
	queriesTotal   int
	queriesBlocked int
	activeRules    int // cached to avoid double GetStats() call in View()

	// Logs
	logLines []string

	// View State
	activeTab  int
	menuFocus  bool
	menuCursor int
	listCursor int

	isLoading bool

	// Allowlist legacy input
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
	return tea.Batch(
		tea.Tick(time.Second, func(t time.Time) tea.Msg {
			return tickMsg(t)
		}),
		func() tea.Msg { return tickMsg(time.Now()) },
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	if m.showForm {
		return m.updateForm(msg)
	}

	switch msg := msg.(type) {
	case reloadDoneMsg:
		if msg.err != nil {
			m.logLines = append(m.logLines, fmt.Sprintf("Reload failed: %v", msg.err))
		} else {
			m.logLines = append(m.logLines, "Reload complete.")
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q":
			if !m.inputMode && !m.showForm {
				return m, tea.Quit
			}
		case "r":
			if !m.inputMode && !m.showForm {
				m.logLines = append(m.logLines, "Reload triggered...")
				return m, func() tea.Msg {
					err := m.svc.Reload()
					return reloadDoneMsg{err: err}
				}
			}
		}

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
				if m.activeTab == 3 {
					m.refreshTable()
				}
			} else if m.inputMode {
				if m.inputText != "" && m.activeTab == 2 {
					if err := m.svc.AddAllowed(m.inputText); err != nil {
						m.logLines = append(m.logLines, fmt.Sprintf("Allow failed: %v", err))
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

		case tea.KeyUp, tea.KeyDown:
			if !m.menuFocus && !m.inputMode && m.activeTab != 3 {
				if msg.Type == tea.KeyUp && m.listCursor > 0 {
					m.listCursor--
				}
				if msg.Type == tea.KeyDown {
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

		if !m.inputMode && !m.menuFocus {
			switch msg.String() {
			case "k":
				if m.activeTab != 3 && m.listCursor > 0 {
					m.listCursor--
				}
			case "j":
				if m.activeTab != 3 {
					m.listCursor++
				}
			case "a":
				if m.activeTab == 2 {
					m.inputMode = true
					m.inputText = ""
				} else if m.activeTab == 3 {
					m.showForm = true
					m.focusIndex = 0
					m.inputs[0].Focus()
					m.inputs[1].Blur()
					return m, textinput.Blink
				}
			case "d":
				if m.activeTab == 2 {
					list, _ := m.svc.ListAllowed()
					if m.listCursor < len(list) {
						if err := m.svc.RemoveAllowed(list[m.listCursor]); err != nil {
							m.logLines = append(m.logLines, fmt.Sprintf("Remove failed: %v", err))
						}
					}
				} else if m.activeTab == 3 {
					sel := m.localTable.SelectedRow()
					if len(sel) > 1 {
						if err := m.svc.RemoveLocalRecord(sel[1]); err != nil {
							m.logLines = append(m.logLines, fmt.Sprintf("Remove record failed: %v", err))
						}
						m.refreshTable()
					}
				}
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.localTable.SetWidth(msg.Width - 4)
		m.localTable.SetHeight(m.height - 20)

	case tickMsg:
		// Single GetStats call — result cached in Model fields for View().
		q, b, r, err := m.svc.GetStats()
		if err != nil {
			m.logLines = append(m.logLines, fmt.Sprintf("Error fetching stats: %v", err))
		} else {
			m.queriesTotal = q
			m.queriesBlocked = b
			m.activeRules = r
			if m.isLoading && r > 0 {
				m.isLoading = false
			}
		}

		newLogs, err := m.svc.GetRecentLogs(50)
		if err == nil {
			m.logLines = newLogs
		}

		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
	}

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
				ip := m.inputs[0].Value()
				domain := m.inputs[1].Value()
				if ip != "" && domain != "" {
					if err := m.svc.AddLocalRecord(domain, ip); err != nil {
						m.logLines = append(m.logLines, fmt.Sprintf("Add record failed: %v", err))
					}
					m.refreshTable()
				}
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

	cmds := make([]tea.Cmd, len(m.inputs))
	for i := range m.inputs {
		m.inputs[i], cmds[i] = m.inputs[i].Update(msg)
	}
	for i := range m.inputs {
		if i == m.focusIndex {
			cmds[i] = tea.Batch(cmds[i], m.inputs[i].Focus())
		} else {
			m.inputs[i].Blur()
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) refreshTable() {
	records, _ := m.svc.ListLocalRecords()
	keys := make([]string, 0, len(records))
	for k := range records {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	rows := []table.Row{}
	for _, domain := range keys {
		rows = append(rows, table.Row{records[domain], domain})
	}
	m.localTable.SetRows(rows)
}

func (m *Model) toggleCurrentSource() {
	sources, _ := m.svc.ListSources()
	if len(sources) > 0 && m.listCursor < len(sources) {
		src := sources[m.listCursor]
		if err := m.svc.ToggleSource(src.Name, !src.Enabled); err != nil {
			m.logLines = append(m.logLines, fmt.Sprintf("Toggle failed: %v", err))
		}
	}
}

func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	header := headerStyle.Width(m.width).Render("0x53 PROTECTION SYSTEM")

	tabs := []string{"DASHBOARD", "LISTS", "ALLOW", "LOCAL"}
	renderedTabs := make([]string, len(tabs))
	for i, t := range tabs {
		style := tabInactive
		if m.menuFocus {
			if m.menuCursor == i {
				style = tabFocus
			}
		} else if m.activeTab == i {
			style = tabActive
		}
		renderedTabs[i] = style.Render(t)
	}
	tabStr := lipgloss.JoinHorizontal(lipgloss.Top, renderedTabs...)

	const fixedHeight = 19
	logHeight := m.height - fixedHeight
	if logHeight < 5 {
		logHeight = 5
	}

	// Width-specific statusStyle computed locally — avoids mutating global state on resize.
	localStatusStyle := statusStyle.Width(m.width/2 - 2)

	content := ""

	if m.showForm {
		content = fmt.Sprintf(
			"Add Local Record:\n\n%s\n\n%s\n\n[ENTER] Next/Submit  [ESC] Cancel",
			m.inputs[0].View(),
			m.inputs[1].View(),
		)
		content = lipgloss.Place(m.width, m.height-5, lipgloss.Center, lipgloss.Center, content)

	} else if m.activeTab == 0 {
		uptime := time.Since(m.startTime).Round(time.Second)
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
			blockPercent(m.queriesTotal, m.queriesBlocked),
			m.queriesTotal,
		)
		statsBox := localStatusStyle.Height(6).Render(stats)

		blStatus := fmt.Sprintf("Active Rules: %d\nSources:      %d", m.activeRules, len(srcs))
		blBox := localStatusStyle.Height(6).Render(blStatus)

		headerBlock := lipgloss.JoinHorizontal(lipgloss.Top, statsBox, blBox)

		linesToShow := logHeight
		start := 0
		if len(m.logLines) > linesToShow {
			start = len(m.logLines) - linesToShow
		}
		logBox := logStyle.
			Height(logHeight).
			Width(m.width - 2).
			Render(strings.Join(m.logLines[start:], "\n"))

		content = lipgloss.JoinVertical(lipgloss.Left, headerBlock, "\nLOGS:", logBox)

	} else if m.activeTab == 1 {
		sources, _ := m.svc.ListSources()

		if m.listCursor >= len(sources) {
			m.listCursor = max(0, len(sources)-1)
		}

		startRow := 0
		if m.listCursor >= logHeight {
			startRow = m.listCursor - logHeight + 1
		}
		endRow := min(startRow+logHeight, len(sources))

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
		allowlist, _ := m.svc.ListAllowed()

		if m.inputMode {
			content = fmt.Sprintf("Add Domain to Allowlist:\n\n> %s_", m.inputText)
			content += "\n\n[ENTER] Save   [ESC] Cancel"
		} else {
			if m.listCursor >= len(allowlist) {
				m.listCursor = max(0, len(allowlist)-1)
			}

			startRow := 0
			if m.listCursor >= logHeight {
				startRow = m.listCursor - logHeight + 1
			}
			endRow := min(startRow+logHeight, len(allowlist))

			var listRows []string
			listRows = append(listRows, "  [A] Add Domain  [D] Delete Selected\n")

			if len(allowlist) == 0 {
				listRows = append(listRows, "\n  (No allowed domains)")
			}

			for i := startRow; i < endRow; i++ {
				cursor := "  "
				if m.listCursor == i {
					cursor = "> "
				}
				line := fmt.Sprintf("%s%s", cursor, allowlist[i])
				if m.listCursor == i {
					line = headerStyle.Render(line)
				}
				listRows = append(listRows, line)
			}
			content = strings.Join(listRows, "\n")
		}

	} else if m.activeTab == 3 {
		content = baseTableStyle.Render(m.localTable.View())
		content += "\n  [A] Add Record  [D] Delete  [R] Soft Reload"
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, "\n", tabStr, "\n", content)
}

func blockPercent(total, blocked int) int {
	if total == 0 {
		return 0
	}
	return (blocked * 100) / total
}
