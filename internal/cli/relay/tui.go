package relay

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"ssh-portal/internal/cli/tui"
)

const (
	maxLogLines       = 500                    // Keep last 500 lines in memory
	topSectionHeight  = 60                     // Percentage of available height for top section (rest goes to logs)
	leftSectionWidth  = 30                     // Percentage of available width for left section (invites), rest goes to right (splices)
	tuiUpdateInterval = 500 * time.Millisecond // Interval for updating TUI content
)

// TUI model for relay
type relayTUIModel struct {
	invitesTable  table.Model
	splicesTable  table.Model
	leftViewport  viewport.Model
	rightViewport viewport.Model
	logViewer     *tui.LogViewer
	cancel        context.CancelFunc
	width         int
	height        int
	ready         bool
}

func newRelayTUIModel(logWriter *tui.LogTailWriter, cancel context.CancelFunc) *relayTUIModel {
	return &relayTUIModel{
		logViewer: tui.NewLogViewer(logWriter),
		cancel:    cancel,
	}
}

func (m *relayTUIModel) Init() tea.Cmd {
	// Initialize log viewer and start ticker for updating top content
	return tea.Batch(
		m.logViewer.Init(),
		tea.Tick(tuiUpdateInterval, func(time.Time) tea.Msg {
			return updateTopContentMsg{}
		}),
	)
}

type updateTopContentMsg struct{}

func (m *relayTUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			// Signal shutdown before quitting
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		// Split the screen: top section for invites/splices, bottom section for logs
		borderHeight := 2 // top + bottom border per section
		borderWidth := 2  // left + right border
		availableHeight := msg.Height - (borderHeight * 2)

		// Calculate heights based on percentage split
		topHeight := (availableHeight * topSectionHeight) / 100
		bottomHeight := availableHeight - topHeight

		// Split top section into left (invites) and right (splices)
		// Reserve space for divider (3 chars: space + divider + space) and borders
		availableWidth := msg.Width - borderWidth
		// Calculate widths based on percentage split
		leftWidth := (availableWidth * leftSectionWidth) / 100
		rightWidth := availableWidth - leftWidth - 3 // -3 for divider and padding

		// Reserve some height for header/info, rest for table
		tableHeight := topHeight - 4
		if tableHeight < 3 {
			tableHeight = 3
		}

		if !m.ready {
			m.invitesTable = NewInvitesTable(leftWidth, tableHeight)
			m.splicesTable = NewSplicesTable(rightWidth, tableHeight)
			m.leftViewport = viewport.New(leftWidth, topHeight)
			m.rightViewport = viewport.New(rightWidth, topHeight)
			m.width = msg.Width
			m.height = msg.Height
			m.ready = true
		} else {
			m.invitesTable = UpdateInvitesTable(m.invitesTable, leftWidth, tableHeight)
			m.splicesTable = UpdateSplicesTable(m.splicesTable, rightWidth, tableHeight)
			m.leftViewport.Width = leftWidth
			m.leftViewport.Height = topHeight
			m.rightViewport.Width = rightWidth
			m.rightViewport.Height = topHeight
		}

		// Update top content with current data
		m.updateTopContent()

		// Update log viewer size
		m.logViewer.SetSize(msg.Width, bottomHeight)

		// Handle table and viewport updates
		var invitesCmd, splicesCmd, leftCmd, rightCmd tea.Cmd
		m.invitesTable, invitesCmd = m.invitesTable.Update(msg)
		if invitesCmd != nil {
			cmds = append(cmds, invitesCmd)
		}
		m.splicesTable, splicesCmd = m.splicesTable.Update(msg)
		if splicesCmd != nil {
			cmds = append(cmds, splicesCmd)
		}
		m.leftViewport, leftCmd = m.leftViewport.Update(msg)
		if leftCmd != nil {
			cmds = append(cmds, leftCmd)
		}
		m.rightViewport, rightCmd = m.rightViewport.Update(msg)
		if rightCmd != nil {
			cmds = append(cmds, rightCmd)
		}

	case updateTopContentMsg:
		m.updateTopContent()
		return m, tea.Tick(time.Millisecond*500, func(time.Time) tea.Msg {
			return updateTopContentMsg{}
		})

	default:
		// Handle table and viewport updates
		if m.ready {
			var invitesCmd, splicesCmd, leftCmd, rightCmd tea.Cmd
			m.invitesTable, invitesCmd = m.invitesTable.Update(msg)
			if invitesCmd != nil {
				cmds = append(cmds, invitesCmd)
			}
			m.splicesTable, splicesCmd = m.splicesTable.Update(msg)
			if splicesCmd != nil {
				cmds = append(cmds, splicesCmd)
			}
			m.leftViewport, leftCmd = m.leftViewport.Update(msg)
			if leftCmd != nil {
				cmds = append(cmds, leftCmd)
			}
			m.rightViewport, rightCmd = m.rightViewport.Update(msg)
			if rightCmd != nil {
				cmds = append(cmds, rightCmd)
			}
		}

		// Handle log viewer updates
		logCmd, handled := m.logViewer.Update(msg)
		if handled && logCmd != nil {
			cmds = append(cmds, logCmd)
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *relayTUIModel) updateTopContent() {
	if !m.ready {
		return
	}

	// Update invites table with current data
	// Use viewport width instead of table width to ensure correct sizing
	invitesTableWidth := m.leftViewport.Width
	if invitesTableWidth < 20 {
		invitesTableWidth = 20
	}
	invitesTableHeight := m.leftViewport.Height - 4 // Reserve space for title/info
	if invitesTableHeight < 3 {
		invitesTableHeight = 3
	}
	m.invitesTable = UpdateInvitesTable(m.invitesTable, invitesTableWidth, invitesTableHeight)

	// Update splices table with current data
	// Use viewport width instead of table width to ensure correct sizing
	splicesTableWidth := m.rightViewport.Width
	if splicesTableWidth < 20 {
		splicesTableWidth = 20
	}
	splicesTableHeight := m.rightViewport.Height - 4 // Reserve space for title/info
	if splicesTableHeight < 3 {
		splicesTableHeight = 3
	}
	m.splicesTable = UpdateSplicesTable(m.splicesTable, splicesTableWidth, splicesTableHeight)

	// Render left pane: invites table
	leftContent := RenderLeftPaneContent(m.leftViewport.Width, m.invitesTable)
	m.leftViewport.SetContent(leftContent)

	// Render right pane: splices table
	rightContent := RenderRightPaneContent(m.rightViewport.Width, m.splicesTable)
	m.rightViewport.SetContent(rightContent)
}

func (m *relayTUIModel) View() string {
	if !m.ready {
		return "\n  Initializing..."
	}

	// Header spans full width
	header := tui.RenderTitleBar("Relay", m.width-2)

	// Invisible borders to maintain spacing
	splitStyle := lipgloss.NewStyle().
		Border(lipgloss.HiddenBorder())

	// Get left and right viewport content
	leftContent := m.leftViewport.View()
	rightContent := m.rightViewport.View()

	// Create vertical divider style
	dividerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240"))

	// Split content into lines and join with divider
	leftLines := strings.Split(leftContent, "\n")
	rightLines := strings.Split(rightContent, "\n")

	// Determine the actual width of the left pane
	leftWidth := 0
	for _, line := range leftLines {
		if line != "" {
			leftWidth = lipgloss.Width(line)
			break
		}
	}
	if leftWidth == 0 {
		leftWidth = m.leftViewport.Width
	}

	// Ensure both have the same number of lines
	maxLines := len(leftLines)
	if len(rightLines) > maxLines {
		maxLines = len(rightLines)
	}

	var combinedLines []string
	for i := 0; i < maxLines; i++ {
		leftLine := ""
		if i < len(leftLines) {
			leftLine = leftLines[i]
		}
		rightLine := ""
		if i < len(rightLines) {
			rightLine = rightLines[i]
		}

		// Pad left line to viewport width, then add divider, then right line
		leftDisplayWidth := lipgloss.Width(leftLine)
		if leftWidth > 0 && leftDisplayWidth < leftWidth {
			leftLine += strings.Repeat(" ", leftWidth-leftDisplayWidth)
		}

		divider := dividerStyle.Render("│")
		combinedLine := leftLine + " " + divider + " " + rightLine
		combinedLines = append(combinedLines, combinedLine)
	}

	topRow := strings.Join(combinedLines, "\n")
	bottomContent := m.logViewer.View()

	topSection := splitStyle.
		Width(m.width - 2).
		Render(topRow)

	bottomSection := splitStyle.
		Width(m.width - 2).
		Render(bottomContent)

	result := lipgloss.JoinVertical(lipgloss.Left, header, topSection, bottomSection)
	lines := strings.Split(result, "\n")
	expectedHeight := m.height
	if len(lines) > expectedHeight {
		result = strings.Join(lines[:expectedHeight], "\n")
	}

	return result
}

// startTUI starts the TUI in a goroutine and sets up log capture
// When the TUI quits, it calls cancel to signal server shutdown
// Returns a channel that will be closed when the TUI goroutine finishes
func startTUI(ctx context.Context, cancel context.CancelFunc) (<-chan struct{}, error) {
	originalOutput := log.Writer()

	// Create log writer
	logWriter := tui.NewLogTailWriter(maxLogLines)

	// Redirect logs to the log writer
	log.SetOutput(logWriter)

	// Create and start the TUI program
	model := newRelayTUIModel(logWriter, cancel)
	p := tea.NewProgram(model, tea.WithAltScreen())

	// Channel to signal when TUI goroutine finishes
	done := make(chan struct{})

	// Run TUI in a goroutine
	go func() {
		defer func() {
			// Restore original log output when TUI exits
			log.SetOutput(originalOutput)
			close(done)
		}()
		if _, err := p.Run(); err != nil {
			log.Printf("TUI error: %v", err)
		}
		// Signal shutdown when TUI exits
		cancel()
	}()

	return done, nil
}
