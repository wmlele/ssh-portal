package sender

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/spinner"
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
	leftSectionWidth = 70  // Percentage of available width for left section (ports), rest goexfs to right (state)
)

// TUI model for sender
type senderTUIModel struct {
	portsTable        table.Model
	reversePortsTable table.Model
	activeTable       int // 0 = direct forwards, 1 = reverse forwards
	leftViewport      viewport.Model
	rightViewport     viewport.Model
	logViewer         *tui.LogViewer
	help              help.Model
	connectingSpinner spinner.Model
	connectedSpinner  spinner.Model
	cancel            context.CancelFunc
	width             int
	height            int
	ready             bool
	// Form state
	showForm    bool
	portForm    *huh.Form
	formKind    string // "local" or "reverse"
	formData    PortForwardForm
	revFormData ReverseForwardForm
}

func newSenderTUIModel(logWriter *tui.LogTailWriter, cancel context.CancelFunc) *senderTUIModel {
	connectingSp := spinner.New()
	connectingSp.Spinner = spinner.Dot
	connectingSp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("62"))

	connectedSp := spinner.New()
	connectedSp.Spinner = spinner.Points
	connectedSp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("62"))

	return &senderTUIModel{
		logViewer:         tui.NewLogViewer(logWriter),
		help:              help.New(),
		connectingSpinner: connectingSp,
		connectedSpinner:  connectedSp,
		cancel:            cancel,
	}
}

func (m *senderTUIModel) Init() tea.Cmd {
	// Initialize log viewer and start ticker for updating top content
	return tea.Batch(
		m.logViewer.Init(),
		m.connectingSpinner.Tick,
		m.connectedSpinner.Tick,
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
		// Always update log viewer first (but don't block on it)
		logCmd, _ := m.logViewer.Update(msg)
		if logCmd != nil {
			cmds = append(cmds, logCmd)
		}

		// If the form is open, give it first crack at key events
		if m.showForm && m.portForm != nil {
			var formCmd tea.Cmd
			var updatedModel tea.Model
			updatedModel, formCmd = m.portForm.Update(msg)
			if updatedForm, ok := updatedModel.(*huh.Form); ok {
				m.portForm = updatedForm

				// If the form just completed, perform submission
				if m.portForm.State == huh.StateCompleted {
					if m.formKind == "reverse" {
						if m.revFormData.RemotePort != "" && m.revFormData.LocalAddr != "" && m.revFormData.LocalPort != "" {
							p, err := strconv.Atoi(m.revFormData.RemotePort)
							if err != nil || p < 0 || p > 65535 {
								log.Printf("Invalid remote port: %s", m.revFormData.RemotePort)
							} else {
								remoteAddr := m.revFormData.RemoteAddr
								if remoteAddr == "" {
									remoteAddr = "0.0.0.0"
								}
								localTarget := BuildLocalTarget(m.revFormData.LocalAddr, m.revFormData.LocalPort)
								id, actual, err := StartReverseForward(remoteAddr, uint32(p), localTarget)
								if err != nil || id == "" {
									log.Printf("Failed to create reverse forward: %v", err)
								} else {
									log.Printf("Reverse forward created on %s:%d -> %s", remoteAddr, actual, localTarget)
									m.activeTable = 1
									m.updateTableFocus()
								}
							}
							m.showForm = false
							m.portForm = nil
							m.formKind = ""
							m.revFormData = ReverseForwardForm{}
							m.updateTopContent()
							if formCmd != nil {
								return m, tea.Batch(formCmd, tea.Tick(time.Millisecond*500, func(time.Time) tea.Msg {
									return updateTopContentMsg{}
								}))
							}
							return m, tea.Tick(time.Millisecond*500, func(time.Time) tea.Msg {
								return updateTopContentMsg{}
							})
						}
					} else {
						if m.formData.LocalPort != "" && m.formData.RemoteAddr != "" && m.formData.RemotePort != "" {
							listen := BuildListenAddress(m.formData.LocalAddr, m.formData.LocalPort)
							target := BuildTargetAddress(m.formData.RemoteAddr, m.formData.RemotePort)
							id := RegisterPortForward(listen, target)
							if id == "" {
								log.Printf("Failed to create port forward - SSH client may not be connected")
							}
							m.showForm = false
							m.portForm = nil
							m.formKind = ""
							m.formData = PortForwardForm{}
							m.updateTopContent()
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
				m.formKind = ""
				m.formData = PortForwardForm{}
				m.revFormData = ReverseForwardForm{}
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
		case "tab":
			// Switch between direct and reverse forwards tables
			if !m.showForm && m.ready {
				m.activeTable = 1 - m.activeTable // Toggle between 0 and 1
				m.updateTableFocus()
				m.updateTopContent()
			}
		case "l":
			// Show form to create new port forward
			if !m.showForm && m.ready {
				m.showForm = true
				m.formKind = "local"
				m.formData = PortForwardForm{}
				m.portForm = NewPortForwardForm(m.leftViewport.Width, &m.formData)
				// Stop the automatic content update ticker when form is shown
				return m, m.portForm.Init()
			}
		case "r":
			// Show form to create new reverse port forward
			if !m.showForm && m.ready {
				m.showForm = true
				m.formKind = "reverse"
				m.revFormData = ReverseForwardForm{}
				m.portForm = NewReverseForwardForm(m.leftViewport.Width, &m.revFormData)
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
			// Let active table handle navigation keys (up/down) when it's focused
			if !m.showForm && m.ready {
				var tableCmd tea.Cmd
				if m.activeTable == 0 {
					m.portsTable, tableCmd = m.portsTable.Update(msg)
				} else {
					m.reversePortsTable, tableCmd = m.reversePortsTable.Update(msg)
				}
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
		// Reserve space for divider (3 chars: space + divider + space) and borders
		availableWidth := msg.Width - borderWidth
		// Calculate widths based on percentage split
		leftWidth := (availableWidth * leftSectionWidth) / 100
		rightWidth := availableWidth - leftWidth - 3 // -3 for divider and padding

		// Reserve height for two stacked tables with headers
		availableTableHeight := topHeight - 6 // Reserve ~6 lines for headers and help
		if availableTableHeight < 6 {
			availableTableHeight = 6
		}
		topTableHeight := availableTableHeight / 2
		bottomTableHeight := availableTableHeight - topTableHeight

		if !m.ready {
			m.portsTable = NewPortsTable(leftWidth, topTableHeight)
			m.reversePortsTable = NewReverseForwardsTable(leftWidth, bottomTableHeight)
			m.leftViewport = viewport.New(leftWidth, topHeight)
			m.rightViewport = viewport.New(rightWidth, topHeight)
			m.width = msg.Width
			m.height = msg.Height
			m.ready = true
			m.activeTable = 0 // Start with direct forwards focused
			m.updateTableFocus()
		} else {
			m.portsTable = UpdatePortsTable(m.portsTable, leftWidth, topTableHeight)
			m.reversePortsTable = UpdateReverseForwardsTable(m.reversePortsTable, leftWidth, bottomTableHeight)
			m.leftViewport.Width = leftWidth
			m.leftViewport.Height = topHeight
			m.rightViewport.Width = rightWidth
			m.rightViewport.Height = topHeight
			m.updateTableFocus()
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
		var tableCmd, table2Cmd, leftCmd, rightCmd tea.Cmd
		m.portsTable, tableCmd = m.portsTable.Update(msg)
		if tableCmd != nil {
			cmds = append(cmds, tableCmd)
		}
		m.reversePortsTable, table2Cmd = m.reversePortsTable.Update(msg)
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
		// Handle spinner updates
		var connectingSpinnerCmd, connectedSpinnerCmd tea.Cmd
		m.connectingSpinner, connectingSpinnerCmd = m.connectingSpinner.Update(msg)
		if connectingSpinnerCmd != nil {
			cmds = append(cmds, connectingSpinnerCmd)
		}
		m.connectedSpinner, connectedSpinnerCmd = m.connectedSpinner.Update(msg)
		if connectedSpinnerCmd != nil {
			cmds = append(cmds, connectedSpinnerCmd)
		}

		// Handle log viewer updates first (should always process)
		logCmd, handled := m.logViewer.Update(msg)
		if handled && logCmd != nil {
			cmds = append(cmds, logCmd)
		}

		// Delegate message to form when it's open
		if m.showForm && m.portForm != nil {
			if cmd, handled := m.handleFormMessage(msg); handled {
				// Even if form handled the message, include any log commands
				if cmd != nil {
					return m, tea.Batch(append(cmds, cmd)...)
				}
				return m, tea.Batch(cmds...)
			}
		}

		// Handle table and viewport updates (only when form is not shown)
		// Note: Table navigation keys are handled in the KeyMsg case above
		if m.ready && !m.showForm {
			var tableCmd, table2Cmd, leftCmd, rightCmd tea.Cmd
			m.portsTable, tableCmd = m.portsTable.Update(msg)
			if tableCmd != nil {
				cmds = append(cmds, tableCmd)
			}
			m.reversePortsTable, table2Cmd = m.reversePortsTable.Update(msg)
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
	}

	return m, tea.Batch(cmds...)
}

// updateTableFocus updates which table has focus based on activeTable
func (m *senderTUIModel) updateTableFocus() {
	if !m.ready {
		return
	}
	if m.activeTable == 0 {
		m.portsTable.Focus()
		m.reversePortsTable.Blur()
	} else {
		m.portsTable.Blur()
		m.reversePortsTable.Focus()
	}
}

func (m *senderTUIModel) deleteSelectedPortForward() error {
	if !m.ready {
		return fmt.Errorf("not ready")
	}

	// Get the selected row from the active table
	var selectedRow []string
	if m.activeTable == 0 {
		selectedRow = m.portsTable.SelectedRow()
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
	} else {
		selectedRow = m.reversePortsTable.SelectedRow()
		if len(selectedRow) < 2 {
			return fmt.Errorf("invalid row selected")
		}
		remoteListen := selectedRow[0]
		localTarget := selectedRow[1]

		// Find and delete the reverse forward
		revs := GetAllReverseForwards()
		for _, rf := range revs {
			expectedRemote := fmt.Sprintf("%s:%d", rf.BindAddr, rf.BindPort)
			if expectedRemote == remoteListen && rf.LocalTarget == localTarget {
				if err := StopReverseForward(rf.ID); err != nil {
					return err
				}
				return nil
			}
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
			if m.formKind == "reverse" {
				if m.revFormData.RemotePort != "" && m.revFormData.LocalAddr != "" && m.revFormData.LocalPort != "" {
					p, err := strconv.Atoi(m.revFormData.RemotePort)
					if err != nil || p < 0 || p > 65535 {
						log.Printf("Invalid remote port: %s", m.revFormData.RemotePort)
					} else {
						remoteAddr := m.revFormData.RemoteAddr
						if remoteAddr == "" {
							remoteAddr = "0.0.0.0"
						}
						localTarget := BuildLocalTarget(m.revFormData.LocalAddr, m.revFormData.LocalPort)
						id, actual, err := StartReverseForward(remoteAddr, uint32(p), localTarget)
						if err != nil || id == "" {
							log.Printf("Failed to create reverse forward: %v", err)
						} else {
							log.Printf("Reverse forward created on %s:%d -> %s", remoteAddr, actual, localTarget)
							m.activeTable = 1
							m.updateTableFocus()
						}
					}
					m.showForm = false
					m.portForm = nil
					m.formKind = ""
					m.revFormData = ReverseForwardForm{}
					m.updateTopContent()
					return tea.Tick(time.Millisecond*500, func(time.Time) tea.Msg {
						return updateTopContentMsg{}
					}), true
				}
			} else {
				if m.formData.LocalPort != "" && m.formData.RemoteAddr != "" && m.formData.RemotePort != "" {
					listen := BuildListenAddress(m.formData.LocalAddr, m.formData.LocalPort)
					target := BuildTargetAddress(m.formData.RemoteAddr, m.formData.RemotePort)
					id := RegisterPortForward(listen, target)
					if id == "" {
						log.Printf("Failed to create port forward - SSH client may not be connected")
					}
					m.showForm = false
					m.portForm = nil
					m.formKind = ""
					m.formData = PortForwardForm{}
					m.updateTopContent()
					return tea.Tick(time.Millisecond*500, func(time.Time) tea.Msg {
						return updateTopContentMsg{}
					}), true
				}
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
		titleText := "New Port Forward"
		if m.formKind == "reverse" {
			titleText = "New Reverse Port Forward"
		}
		title := titleStyle.Render(titleText)

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
		// Update both tables with current data
		tableWidth := m.leftViewport.Width
		if tableWidth < 20 {
			tableWidth = 20
		}
		availableTableHeight := m.leftViewport.Height - 6 // Reserve space for headers/help
		if availableTableHeight < 6 {
			availableTableHeight = 6
		}
		topTableHeight := availableTableHeight / 2
		bottomTableHeight := availableTableHeight - topTableHeight
		m.portsTable = UpdatePortsTable(m.portsTable, tableWidth, topTableHeight)
		m.reversePortsTable = UpdateReverseForwardsTable(m.reversePortsTable, tableWidth, bottomTableHeight)
		m.updateTableFocus()

		// Update help width
		m.help.Width = m.leftViewport.Width
		// Render left pane: both tables with headers + help
		leftContent = RenderLeftPaneContent(m.leftViewport.Width, m.portsTable, m.reversePortsTable, m.help)
	}
	m.leftViewport.SetContent(leftContent)

	// Render right side: current state information
	rightContent := RenderStateView(m.rightViewport.Width, m.connectingSpinner, m.connectedSpinner)
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
		combinedLine := leftLine + " " + divider + " " + rightLine
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
