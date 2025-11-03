package sender

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"ssh-portal/internal/cli/tui"
)

const (
	maxLogLines      = 500 // Keep last 500 lines in memory
	topSectionHeight = 70  // Percentage of available height for top section (rest goes to logs)
	leftSectionWidth = 70  // Percentage of available width for left section (ports), rest goes to right (state)
)

// TUI model for sender
type senderTUIModel struct {
	portsTable    table.Model
	leftViewport  viewport.Model
	rightViewport viewport.Model
	logViewer     *tui.LogViewer
	cancel        context.CancelFunc
	width         int
	height        int
	ready         bool
	// Form state
	showForm bool
	portForm *huh.Form
	formData PortForwardForm
}

func newSenderTUIModel(logWriter *tui.LogTailWriter, cancel context.CancelFunc) *senderTUIModel {
	return &senderTUIModel{
		logViewer: tui.NewLogViewer(logWriter),
		cancel:    cancel,
	}
}

func (m *senderTUIModel) Init() tea.Cmd {
	// Initialize log viewer and start ticker for updating top content
	return tea.Batch(
		m.logViewer.Init(),
		tea.Tick(time.Millisecond*500, func(time.Time) tea.Msg {
			return updateTopContentMsg{}
		}),
	)
}

type updateTopContentMsg struct{}

func (m *senderTUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		case "n":
			// Show form to create new port forward
			if !m.showForm && m.ready {
				m.showForm = true
				m.formData = PortForwardForm{}
				m.portForm = NewPortForwardForm(m.leftViewport.Width, &m.formData)
				// Stop the automatic content update ticker when form is shown
				return m, m.portForm.Init()
			}
		case "esc":
			// Cancel form
			if m.showForm {
				m.showForm = false
				m.portForm = nil
				m.formData = PortForwardForm{}
				m.updateTopContent()
				// Restart the automatic content update ticker
				return m, tea.Tick(time.Millisecond*500, func(time.Time) tea.Msg {
					return updateTopContentMsg{}
				})
			}
		case "enter":
			// Submit form if shown and completed
			if m.showForm && m.portForm != nil {
				if m.portForm.State == huh.StateCompleted {
					// Form is completed, create the port forward
					if m.formData.LocalPort != "" && m.formData.RemoteAddr != "" && m.formData.RemotePort != "" {
						listen := BuildListenAddress(m.formData.LocalPort)
						target := BuildTargetAddress(m.formData.RemoteAddr, m.formData.RemotePort)
						RegisterPortForward(listen, target)
						// TODO: Start actual port forwarding here
						// This requires access to the SSH client
						m.showForm = false
						m.portForm = nil
						m.formData = PortForwardForm{}
						m.updateTopContent()
					}
				}
			}
		}

	case tea.WindowSizeMsg:
		// Split the screen: top section for state, bottom section for logs
		borderHeight := 2 // top + bottom border per section
		borderWidth := 2  // left + right border
		availableHeight := msg.Height - (borderHeight * 2)

		// Calculate heights based on percentage split
		topHeight := (availableHeight * topSectionHeight) / 100
		bottomHeight := availableHeight - topHeight

		// Split top section into left (port forwards) and right (state info)
		// Reserve space for divider (1 char) and borders
		availableWidth := msg.Width - borderWidth
		// Calculate widths based on percentage split
		leftWidth := (availableWidth * leftSectionWidth) / 100
		rightWidth := availableWidth - leftWidth - 1 // -1 for divider

		// Reserve some height for header/info in left pane, rest for table
		tableHeight := topHeight - 4 // Reserve ~4 lines for title and info
		if tableHeight < 3 {
			tableHeight = 3
		}

		if !m.ready {
			m.portsTable = NewPortsTable(leftWidth, tableHeight)
			m.leftViewport = viewport.New(leftWidth, topHeight)
			m.rightViewport = viewport.New(rightWidth, topHeight)
			m.width = msg.Width
			m.height = msg.Height
			m.ready = true
		} else {
			m.portsTable = UpdatePortsTable(m.portsTable, leftWidth, tableHeight)
			m.leftViewport.Width = leftWidth
			m.leftViewport.Height = topHeight
			m.rightViewport.Width = rightWidth
			m.rightViewport.Height = topHeight
			// Update form width if form is shown
			if m.showForm && m.portForm != nil {
				m.portForm = NewPortForwardForm(leftWidth, &m.formData)
				// Re-initialize form with new width
				formCmd := m.portForm.Init()
				if formCmd != nil {
					cmds = append(cmds, formCmd)
				}
			}
		}

		// Update top content with current data
		m.updateTopContent()

		// Update log viewer size
		m.logViewer.SetSize(msg.Width, bottomHeight)

		// Handle table and viewport updates
		var tableCmd, leftCmd, rightCmd tea.Cmd
		m.portsTable, tableCmd = m.portsTable.Update(msg)
		if tableCmd != nil {
			cmds = append(cmds, tableCmd)
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
		// Only update content if form is not shown (form handles its own updates)
		if !m.showForm {
			m.updateTopContent()
		}
		// Continue ticker only if form is not shown
		if !m.showForm {
			return m, tea.Tick(time.Millisecond*500, func(time.Time) tea.Msg {
				return updateTopContentMsg{}
			})
		}
		// If form is shown, don't schedule another tick
		return m, nil

	default:
		// Handle form updates first if form is shown (give priority to form input)
		if m.showForm && m.portForm != nil {
			var formCmd tea.Cmd
			var updatedModel tea.Model
			updatedModel, formCmd = m.portForm.Update(msg)
			if updatedForm, ok := updatedModel.(*huh.Form); ok {
				m.portForm = updatedForm
				// Check if form was just completed
				if m.portForm.State == huh.StateCompleted {
					// Form is completed, create the port forward
					if m.formData.LocalPort != "" && m.formData.RemoteAddr != "" && m.formData.RemotePort != "" {
						listen := BuildListenAddress(m.formData.LocalPort)
						target := BuildTargetAddress(m.formData.RemoteAddr, m.formData.RemotePort)
						RegisterPortForward(listen, target)
						// TODO: Start actual port forwarding here
						// This requires access to the SSH client
						m.showForm = false
						m.portForm = nil
						m.formData = PortForwardForm{}
						// Restart ticker now that form is closed
						cmds = append(cmds, tea.Tick(time.Millisecond*500, func(time.Time) tea.Msg {
							return updateTopContentMsg{}
						}))
					}
				}
				// Update content when form changes (but only if form is still active)
				if m.showForm {
					m.updateTopContent()
				}
			}
			if formCmd != nil {
				cmds = append(cmds, formCmd)
			}
			// Form consumes all messages when active, don't process other updates
			return m, tea.Batch(cmds...)
		}

		// Handle table and viewport updates (only when form is not shown)
		if m.ready && !m.showForm {
			var tableCmd, leftCmd, rightCmd tea.Cmd
			m.portsTable, tableCmd = m.portsTable.Update(msg)
			if tableCmd != nil {
				cmds = append(cmds, tableCmd)
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

func (m *senderTUIModel) updateTopContent() {
	if !m.ready {
		return
	}

	var leftContent string
	if m.showForm && m.portForm != nil {
		// Show form
		titleStyle := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("62")).
			MarginBottom(1)
		title := titleStyle.Render("New Port Forward")
		formView := m.portForm.View()
		helpText := lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			MarginTop(1).
			Render("Press Enter to submit, Esc to cancel")
		leftContent = lipgloss.JoinVertical(
			lipgloss.Left,
			title,
			formView,
			helpText,
		)
	} else {
		// Update ports table with current data
		// Use viewport width instead of table width to ensure correct sizing
		tableWidth := m.leftViewport.Width
		if tableWidth < 20 {
			tableWidth = 20
		}
		tableHeight := m.leftViewport.Height - 4 // Reserve space for title/info
		if tableHeight < 3 {
			tableHeight = 3
		}
		m.portsTable = UpdatePortsTable(m.portsTable, tableWidth, tableHeight)
		// Render left pane: header + table
		leftContent = RenderLeftPaneContent(m.leftViewport.Width, m.portsTable)
	}
	m.leftViewport.SetContent(leftContent)

	// Render right side: current state information
	rightContent := RenderStateView(m.rightViewport.Width)
	m.rightViewport.SetContent(rightContent)
}

func (m *senderTUIModel) View() string {
	if !m.ready {
		return "\n  Initializing..."
	}

	// Invisible borders to maintain spacing
	splitStyle := lipgloss.NewStyle().
		Border(lipgloss.HiddenBorder())

	// Header spans full width
	header := tui.RenderTitleBar("Sender", m.width-2)

	// Get left and right viewport content
	leftContent := m.leftViewport.View()
	rightContent := m.rightViewport.View()

	// Create vertical divider style
	dividerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240"))

	// Split content into lines and join with divider
	leftLines := strings.Split(leftContent, "\n")
	rightLines := strings.Split(rightContent, "\n")

	// Determine the actual width of the left pane (use first non-empty line)
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
		// Calculate actual display width (accounting for ANSI codes)
		leftDisplayWidth := lipgloss.Width(leftLine)
		if leftWidth > 0 && leftDisplayWidth < leftWidth {
			leftLine += strings.Repeat(" ", leftWidth-leftDisplayWidth)
		}

		divider := dividerStyle.Render("│")
		combinedLine := leftLine + divider + rightLine
		combinedLines = append(combinedLines, combinedLine)
	}

	topRow := strings.Join(combinedLines, "\n")

	bottomContent := m.logViewer.View()

	// Combine header, top section, and bottom section
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
	model := newSenderTUIModel(logWriter, cancel)
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
