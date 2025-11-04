package receiver

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"ssh-portal/internal/cli/tui"
)

const (
	maxLogLines      = 500 // Keep last 500 lines in memory
	topSectionHeight = 70  // Percentage of available height for top section (rest goes to logs)
	leftSectionWidth = 40  // Percentage of available width for left section (info), rest goes to right (forwards)
)

// TUI model for receiver
type receiverTUIModel struct {
	forwardsTable        table.Model
	reverseForwardsTable table.Model
	leftViewport         viewport.Model
	rightViewport        viewport.Model
	logViewer            *tui.LogViewer
	spinner              spinner.Model
	cancel               context.CancelFunc
	width                int
	height               int
	ready                bool
}

func newReceiverTUIModel(logWriter *tui.LogTailWriter, cancel context.CancelFunc) *receiverTUIModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("62"))

	return &receiverTUIModel{
		logViewer: tui.NewLogViewer(logWriter),
		spinner:   sp,
		cancel:    cancel,
	}
}

func (m *receiverTUIModel) Init() tea.Cmd {
	// Initialize log viewer, spinner, and start ticker for updating top content
	return tea.Batch(
		m.logViewer.Init(),
		m.spinner.Tick,
		tea.Tick(time.Millisecond*100, func(time.Time) tea.Msg {
			return updateTopContentMsg{}
		}),
	)
}

type updateTopContentMsg struct{}

func (m *receiverTUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		// Split the screen: top section for state, bottom section for logs
		borderHeight := 2 // top + bottom border per section
		borderWidth := 2  // left + right border
		availableHeight := msg.Height - (borderHeight * 2)

		// Calculate heights based on percentage split
		topHeight := (availableHeight * topSectionHeight) / 100
		bottomHeight := availableHeight - topHeight

		// Split top section into left (info) and right (forwards table)
		// Reserve space for divider (3 chars: space + divider + space) and borders
		availableWidth := msg.Width - borderWidth
		// Calculate widths based on percentage split
		leftWidth := (availableWidth * leftSectionWidth) / 100
		rightWidth := availableWidth - leftWidth - 3 // -3 for divider and padding

		// Compute table heights for two stacked tables in right pane
		// Reserve ~2 lines for headers between tables
		availableTableHeight := topHeight - 4
		if availableTableHeight < 6 {
			availableTableHeight = 6
		}
		topTableHeight := availableTableHeight / 2
		bottomTableHeight := availableTableHeight - topTableHeight

		if !m.ready {
			m.forwardsTable = NewForwardsTable(rightWidth, topTableHeight)
			m.reverseForwardsTable = NewReverseForwardsTable(rightWidth, bottomTableHeight)
			m.leftViewport = viewport.New(leftWidth, topHeight)
			m.rightViewport = viewport.New(rightWidth, topHeight)
			m.width = msg.Width
			m.height = msg.Height
			m.ready = true
		} else {
			m.forwardsTable = UpdateForwardsTable(m.forwardsTable, rightWidth, topTableHeight)
			m.reverseForwardsTable = UpdateReverseForwardsTable(m.reverseForwardsTable, rightWidth, bottomTableHeight)
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
		var tableCmd, table2Cmd, leftCmd, rightCmd tea.Cmd
		m.forwardsTable, tableCmd = m.forwardsTable.Update(msg)
		if tableCmd != nil {
			cmds = append(cmds, tableCmd)
		}
		m.reverseForwardsTable, table2Cmd = m.reverseForwardsTable.Update(msg)
		if table2Cmd != nil {
			cmds = append(cmds, table2Cmd)
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
		return m, tea.Tick(time.Millisecond*100, func(time.Time) tea.Msg {
			return updateTopContentMsg{}
		})

	default:
		// Handle spinner updates
		var spinnerCmd tea.Cmd
		m.spinner, spinnerCmd = m.spinner.Update(msg)
		if spinnerCmd != nil {
			cmds = append(cmds, spinnerCmd)
		}

		// Handle table and viewport updates
		if m.ready {
			var tableCmd, table2Cmd, leftCmd, rightCmd tea.Cmd
			m.forwardsTable, tableCmd = m.forwardsTable.Update(msg)
			if tableCmd != nil {
				cmds = append(cmds, tableCmd)
			}
			m.reverseForwardsTable, table2Cmd = m.reverseForwardsTable.Update(msg)
			if table2Cmd != nil {
				cmds = append(cmds, table2Cmd)
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

func (m *receiverTUIModel) updateTopContent() {
	if !m.ready {
		return
	}

	// Update tables with current data
	// Use viewport width instead of table width to ensure correct sizing
	tableWidth := m.rightViewport.Width
	if tableWidth < 20 {
		tableWidth = 20
	}
	availableTableHeight := m.rightViewport.Height - 4
	if availableTableHeight < 6 {
		availableTableHeight = 6
	}
	topTableHeight := availableTableHeight / 2
	bottomTableHeight := availableTableHeight - topTableHeight
	m.forwardsTable = UpdateForwardsTable(m.forwardsTable, tableWidth, topTableHeight)
	m.reverseForwardsTable = UpdateReverseForwardsTable(m.reverseForwardsTable, tableWidth, bottomTableHeight)

	// Render left pane: connection info
	leftContent := RenderLeftPaneContent(m.leftViewport.Width, m.spinner)
	m.leftViewport.SetContent(leftContent)

	// Render right pane: direct and reverse forwards with headers
	directCount := len(GetAllDirectTCPIPs())
	reverseCount := len(GetAllReverseTCPIPs())
	headerDirect := tui.RenderDirectionalHeader("R", "21", "L", "62", fmt.Sprintf("%d active", directCount))
	headerReverse := tui.RenderDirectionalHeader("L", "62", "R", "21", fmt.Sprintf("%d active", reverseCount))
	directView := m.forwardsTable.View()
	if directView == "" {
		directView = "  No active forwards"
	}
	reverseView := m.reverseForwardsTable.View()
	if reverseView == "" {
		reverseView = "  No reverse forwards"
	}
	rightContent := lipgloss.JoinVertical(
		lipgloss.Left,
		headerDirect,
		"", // Blank line between R->L header and table
		directView,
		headerReverse,
		"", // Blank line between L->R header and table
		reverseView,
	)
	m.rightViewport.SetContent(rightContent)
}

func (m *receiverTUIModel) View() string {
	if !m.ready {
		return "\n  Initializing..."
	}

	// Header spans full width
	header := tui.RenderTitleBar("Receiver", m.width-2)

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
// When the TUI quits, it calls cancel to signal shutdown
func startTUI(ctx context.Context, cancel context.CancelFunc) error {
	originalOutput := log.Writer()

	// Create log writer
	logWriter := tui.NewLogTailWriter(maxLogLines)

	// Redirect logs to the log writer
	log.SetOutput(logWriter)

	// Create and start the TUI program
	model := newReceiverTUIModel(logWriter, cancel)
	p := tea.NewProgram(model, tea.WithAltScreen())

	// Run TUI in a goroutine
	go func() {
		defer func() {
			// Restore original log output when TUI exits
			log.SetOutput(originalOutput)
		}()
		if _, err := p.Run(); err != nil {
			log.Printf("TUI error: %v", err)
		}
		// Signal shutdown when TUI exits
		cancel()
	}()

	return nil
}
