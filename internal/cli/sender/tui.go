package sender

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"ssh-portal/internal/cli/tui"
)

const (
	maxLogLines      = 500 // Keep last 500 lines in memory
	topSectionHeight = 70  // Percentage of available height for top section (rest goes to logs)
)

// TUI model for sender
type senderTUIModel struct {
	topViewport viewport.Model
	logViewer   *tui.LogViewer
	cancel      context.CancelFunc
	width       int
	height      int
	ready       bool
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
		}

	case tea.WindowSizeMsg:
		// Split the screen: top section for state, bottom section for logs
		borderHeight := 2 // top + bottom border per section
		borderWidth := 2  // left + right border
		availableHeight := msg.Height - (borderHeight * 2)

		// Calculate heights based on percentage split
		topHeight := (availableHeight * topSectionHeight) / 100
		bottomHeight := availableHeight - topHeight

		if !m.ready {
			m.topViewport = viewport.New(msg.Width-borderWidth, topHeight)
			m.width = msg.Width
			m.height = msg.Height
			m.ready = true
		} else {
			m.topViewport.Width = msg.Width - borderWidth
			m.topViewport.Height = topHeight
		}

		// Update top content with current data
		m.updateTopContent()

		// Update log viewer size
		m.logViewer.SetSize(msg.Width, bottomHeight)

		// Handle viewport updates
		var topCmd tea.Cmd
		m.topViewport, topCmd = m.topViewport.Update(msg)
		if topCmd != nil {
			cmds = append(cmds, topCmd)
		}

	case updateTopContentMsg:
		m.updateTopContent()
		return m, tea.Tick(time.Millisecond*500, func(time.Time) tea.Msg {
			return updateTopContentMsg{}
		})

	default:
		// Handle top viewport updates
		if m.ready {
			var topCmd tea.Cmd
			m.topViewport, topCmd = m.topViewport.Update(msg)
			if topCmd != nil {
				cmds = append(cmds, topCmd)
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

	// Use stateview to render the content
	content := RenderStateView(m.topViewport.Width)
	m.topViewport.SetContent(content)
}

func (m *senderTUIModel) View() string {
	if !m.ready {
		return "\n  Initializing..."
	}

	// Invisible borders to maintain spacing
	splitStyle := lipgloss.NewStyle().
		Border(lipgloss.HiddenBorder())

	topContent := m.topViewport.View()
	bottomContent := m.logViewer.View()

	topSection := splitStyle.
		Width(m.width - 2).
		Render(topContent)

	bottomSection := splitStyle.
		Width(m.width - 2).
		Render(bottomContent)

	result := lipgloss.JoinVertical(lipgloss.Left, topSection, bottomSection)
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

