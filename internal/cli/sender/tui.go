package sender

import (
	"context"
	"fmt"
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
		// If the form is open, give it first crack at key events
		if m.showForm && m.portForm != nil {
			var formCmd tea.Cmd
			var updatedModel tea.Model
			updatedModel, formCmd = m.portForm.Update(msg)
			if updatedForm, ok := updatedModel.(*huh.Form); ok {
				m.portForm = updatedForm

				// If the form just completed, perform submission
				if m.portForm.State == huh.StateCompleted {
					if m.formData.LocalPort != "" && m.formData.RemoteAddr != "" && m.formData.RemotePort != "" {
						listen := BuildListenAddress(m.formData.LocalPort)
						target := BuildTargetAddress(m.formData.RemoteAddr, m.formData.RemotePort)
						id := RegisterPortForward(listen, target)
						if id == "" {
							// Port forward creation failed (SSH client may not be connected)
							// Form will remain open to show validation/error state
							log.Printf("Failed to create port forward - SSH client may not be connected")
						}
						m.showForm = false
						m.portForm = nil
						m.formData = PortForwardForm{}
						m.updateTopContent()
						// Restart ticker now that form is closed
						if formCmd != nil {
							return m, tea.Batch(formCmd, tea.Tick(time.Millisecond*500, func(time.Time) tea.Msg {
								return updateTopContentMsg{}
							}))
						}
						return m, tea.Tick(time.Millisecond*500, func(time.Time) tea.Msg {
							return updateTopContentMsg{}
						})
					}
				}
				// Update viewport content to show form changes (input, validation, etc.)
				m.updateTopContent()
			}
			if formCmd != nil {
				return m, formCmd
			}

			// Handle global shortcuts while form is open
			switch msg.String() {
			case "esc":
				// Cancel form
				m.showForm = false
				m.portForm = nil
				m.formData = PortForwardForm{}
				m.updateTopContent()
				// Restart the automatic content update ticker
				return m, tea.Tick(time.Millisecond*500, func(time.Time) tea.Msg {
					return updateTopContentMsg{}
				})
			case "ctrl+c", "q":
				// Signal shutdown before quitting
				if m.cancel != nil {
					m.cancel()
				}
				return m, tea.Quit
			}

			// Key was consumed by form or no extra action required
			return m, nil
		}

		// Handle keys when form is not shown
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
		case "d":
			// Delete selected port forward
			if !m.showForm && m.ready {
				if err := m.deleteSelectedPortForward(); err == nil {
					// Update table content after deletion (maintains focus and cursor position)
					m.updateTopContent()
				}
			}
		default:
			// Let table handle navigation keys (up/down) when it's focused
			if !m.showForm && m.ready {
				var tableCmd tea.Cmd
				m.portsTable, tableCmd = m.portsTable.Update(msg)
				if tableCmd != nil {
					cmds = append(cmds, tableCmd)
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
			// Update form width if form is shown - but preserve form state
			if m.showForm && m.portForm != nil {
				// Don't recreate the form, just update its width
				// The form should handle width changes internally via its Update method
				// Recreating would lose input state
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
		// Delegate message to form when it's open
		if m.showForm && m.portForm != nil {
			if cmd, handled := m.handleFormMessage(msg); handled {
				return m, cmd
			}
		}

		// Handle table and viewport updates (only when form is not shown)
		// Note: Table navigation keys are handled in the KeyMsg case above
		if m.ready && !m.showForm {
			var leftCmd, rightCmd tea.Cmd
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

func (m *senderTUIModel) deleteSelectedPortForward() error {
	if !m.ready {
		return fmt.Errorf("not ready")
	}

	// Get the selected row from the table
	selectedRow := m.portsTable.SelectedRow()
	if len(selectedRow) < 2 {
		return fmt.Errorf("invalid row selected")
	}

	listenAddr := selectedRow[0]
	targetAddr := selectedRow[1]

	// Find and delete the port forward matching these addresses
	forwards := GetAllPortForwards()
	for _, pf := range forwards {
		if pf.Listen == listenAddr && pf.Target == targetAddr {
			UnregisterPortForward(pf.ID)
			return nil
		}
	}

	return fmt.Errorf("port forward not found")
}

func (m *senderTUIModel) handleFormMessage(msg tea.Msg) (tea.Cmd, bool) {
	// Route any message to the form when it is open
	if !(m.showForm && m.portForm != nil) {
		return nil, false
	}

	var cmds []tea.Cmd
	var formCmd tea.Cmd
	var updatedModel tea.Model

	updatedModel, formCmd = m.portForm.Update(msg)
	if updatedForm, ok := updatedModel.(*huh.Form); ok {
		m.portForm = updatedForm

		// Check if form was just completed
		if m.portForm.State == huh.StateCompleted {
			if m.formData.LocalPort != "" && m.formData.RemoteAddr != "" && m.formData.RemotePort != "" {
				listen := BuildListenAddress(m.formData.LocalPort)
				target := BuildTargetAddress(m.formData.RemoteAddr, m.formData.RemotePort)
				id := RegisterPortForward(listen, target)
				if id == "" {
					// Port forward creation failed (SSH client may not be connected)
					log.Printf("Failed to create port forward - SSH client may not be connected")
				}
				m.showForm = false
				m.portForm = nil
				m.formData = PortForwardForm{}
				m.updateTopContent()
				// Restart ticker now that form is closed
				return tea.Tick(time.Millisecond*500, func(time.Time) tea.Msg {
					return updateTopContentMsg{}
				}), true
			}
		}

		// Update viewport content to show form changes
		m.updateTopContent()
	}

	if formCmd != nil {
		cmds = append(cmds, formCmd)
	}

	if len(cmds) > 0 {
		return tea.Batch(cmds...), true
	}

	// Handled with no additional commands
	return nil, true
}

func (m *senderTUIModel) updateTopContent() {
	if !m.ready {
		return
	}

	var leftContent string
	if m.showForm && m.portForm != nil {
		// Show form - let the form handle its own rendering
		// We just need to render it into the viewport
		titleStyle := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("62")).
			MarginBottom(1)
		title := titleStyle.Render("New Port Forward")

		// Get the form view - this will include any validation errors or current state
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
