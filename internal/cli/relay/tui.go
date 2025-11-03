package relay

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	maxLogLines = 500 // Keep last 500 lines in memory
)

// logTailWriter implements io.Writer and captures log messages
type logTailWriter struct {
	mu      sync.Mutex
	lines   []string
	maxSize int
	ch      chan string
}

func newLogTailWriter(maxSize int) *logTailWriter {
	return &logTailWriter{
		lines:   make([]string, 0, maxSize),
		maxSize: maxSize,
		ch:      make(chan string, 100),
	}
}

func (w *logTailWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	line := strings.TrimSuffix(string(p), "\n")

	// Add timestamp to the log line
	timestamp := time.Now().Format("15:04:05")
	timestampedLine := fmt.Sprintf("[%s] %s", timestamp, line)

	w.lines = append(w.lines, timestampedLine)
	if len(w.lines) > w.maxSize {
		w.lines = w.lines[1:]
	}

	select {
	case w.ch <- timestampedLine:
	default:
		// Channel full, drop message
	}

	return len(p), nil
}

func (w *logTailWriter) getContent() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.Join(w.lines, "\n")
}

// TUI model
type tuiModel struct {
	topViewport    viewport.Model
	bottomViewport viewport.Model
	logWriter      *logTailWriter
	cancel         context.CancelFunc
	width          int
	height         int
	ready          bool
}

func newTUIModel(logWriter *logTailWriter, cancel context.CancelFunc) *tuiModel {
	return &tuiModel{
		logWriter: logWriter,
		cancel:    cancel,
	}
}

func (m *tuiModel) Init() tea.Cmd {
	// Start a ticker to periodically check for log updates
	return tea.Tick(time.Millisecond*100, func(time.Time) tea.Msg {
		return logUpdateMsg{}
	})
}

func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		// Split the screen: top half blank, bottom half for logs
		// Account for borders: 2 rows (top + bottom) per section, 2 columns (left + right) per section
		borderHeight := 2 // top + bottom border
		borderWidth := 2  // left + right border
		// Total borders: 2 sections * 2 rows each = 4 rows
		// Available height for content = total - 4 border rows
		availableHeight := msg.Height - (borderHeight * 2)
		topHeight := availableHeight / 2
		bottomHeight := availableHeight - topHeight

		if !m.ready {
			m.topViewport = viewport.New(msg.Width-borderWidth, topHeight)
			m.bottomViewport = viewport.New(msg.Width-borderWidth, bottomHeight)

			m.topViewport.SetContent("") // Top section is blank for now
			m.bottomViewport.SetContent(m.logWriter.getContent())

			m.width = msg.Width
			m.height = msg.Height
			m.ready = true
		} else {
			m.topViewport.Width = msg.Width - borderWidth
			m.topViewport.Height = topHeight
			m.bottomViewport.Width = msg.Width - borderWidth
			m.bottomViewport.Height = bottomHeight
		}

	case logUpdateMsg:
		if m.ready {
			content := m.logWriter.getContent()
			m.bottomViewport.SetContent(content)
			m.bottomViewport.GotoBottom()
		}
		// Continue ticking to check for updates
		return m, tea.Tick(time.Millisecond*500, func(time.Time) tea.Msg {
			return logUpdateMsg{}
		})

	default:
		// Handle viewport updates
		var topCmd, bottomCmd tea.Cmd
		if m.ready {
			m.topViewport, topCmd = m.topViewport.Update(msg)
			m.bottomViewport, bottomCmd = m.bottomViewport.Update(msg)
			if topCmd != nil {
				cmds = append(cmds, topCmd)
			}
			if bottomCmd != nil {
				cmds = append(cmds, bottomCmd)
			}
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *tuiModel) View() string {
	if !m.ready {
		return "\n  Initializing..."
	}

	// Split style
	splitStyle := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240"))

	// Render viewports - don't set height on border style, let content determine it
	topContent := m.topViewport.View()
	bottomContent := m.bottomViewport.View()

	// Border adds 2 columns (left + right)
	// Viewport content is (m.width - 2) wide
	// To get total width = m.width, we need border width to be m.width (lipgloss handles this)
	// But if that's still too wide, try m.width - 2 so borders add up to m.width
	topSection := splitStyle.
		Width(m.width - 2).
		Render(topContent)

	bottomSection := splitStyle.
		Width(m.width - 2).
		Render(bottomContent)

	// Join sections vertically - lipgloss.JoinVertical adds newlines, so we get exact fit
	result := lipgloss.JoinVertical(lipgloss.Left, topSection, bottomSection)

	// Ensure we don't exceed terminal height by trimming any extra trailing content
	// Count lines to verify we match expected height
	lines := strings.Split(result, "\n")
	expectedHeight := m.height
	if len(lines) > expectedHeight {
		// Trim to exact height
		result = strings.Join(lines[:expectedHeight], "\n")
	}

	return result
}

// logUpdateMsg is a message sent when logs are updated
type logUpdateMsg struct{}

// startTUI starts the TUI in a goroutine and sets up log capture
// When the TUI quits, it calls cancel to signal server shutdown
func startTUI(ctx context.Context, cancel context.CancelFunc, logWriter *logTailWriter) error {
	// Store original log output
	originalOutput := log.Writer()

	// Set the custom log writer (only to TUI when interactive mode is on)
	// This prevents logs from appearing below the TUI window
	log.SetOutput(logWriter)

	// Create and start the TUI program
	model := newTUIModel(logWriter, cancel)
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
