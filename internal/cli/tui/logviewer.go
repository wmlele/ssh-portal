package tui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"ssh-portal/internal/version"
)

// RenderTitleBar renders a two-line header with software name and colored bar
// moduleName is the module name (e.g., "Receiver", "Relay")
// width is the width of the header
func RenderTitleBar(moduleName string, width int) string {
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("62")).
		Padding(0, 1)

	infoStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240"))

	barStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("62")).
		Width(width).
		Height(1)

	titleText := headerStyle.Render(fmt.Sprintf("SSH Portal - %s", moduleName))
	versionText := infoStyle.Render(version.String())
	title := fmt.Sprintf("%s %s", titleText, versionText)
	bar := barStyle.Render(strings.Repeat(" ", width))
	return lipgloss.JoinVertical(lipgloss.Left, title, bar)
}

const (
	DefaultMaxLogLines = 500 // Default number of log lines to keep in memory
)

// LogTailWriter implements io.Writer and captures log messages with timestamps
type LogTailWriter struct {
	mu      sync.Mutex
	lines   []string
	maxSize int
	ch      chan string
}

// NewLogTailWriter creates a new log tail writer
func NewLogTailWriter(maxSize int) *LogTailWriter {
	if maxSize <= 0 {
		maxSize = DefaultMaxLogLines
	}
	return &LogTailWriter{
		lines:   make([]string, 0, maxSize),
		maxSize: maxSize,
		ch:      make(chan string, 100),
	}
}

func (w *LogTailWriter) Write(p []byte) (n int, err error) {
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

func (w *LogTailWriter) GetContent() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.Join(w.lines, "\n")
}

// LogViewer is a component that handles the bottom log viewport
// It can be embedded in a parent model that handles the overall TUI layout
type LogViewer struct {
	viewport  viewport.Model
	logWriter *LogTailWriter
	width     int
	height    int
	ready     bool
}

// NewLogViewer creates a new log viewer component
func NewLogViewer(logWriter *LogTailWriter) *LogViewer {
	return &LogViewer{
		logWriter: logWriter,
	}
}

// Init initializes the log viewer
func (lv *LogViewer) Init() tea.Cmd {
	return tea.Tick(time.Millisecond*500, func(time.Time) tea.Msg {
		return logUpdateMsg{}
	})
}

// Update updates the log viewer component
func (lv *LogViewer) Update(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// This will be called by parent, but we handle our viewport sizing
		// when SetSize is called
		return nil, false

	case logUpdateMsg:
		if lv.ready {
			content := lv.logWriter.GetContent()
			wrappedContent := lipgloss.NewStyle().
				Width(lv.viewport.Width).
				Render(content)
			lv.viewport.SetContent(wrappedContent)
			lv.viewport.GotoBottom()
		}
		return tea.Tick(time.Millisecond*500, func(time.Time) tea.Msg {
			return logUpdateMsg{}
		}), true

	default:
		if lv.ready {
			var cmd tea.Cmd
			lv.viewport, cmd = lv.viewport.Update(msg)
			return cmd, cmd != nil
		}
		return nil, false
	}
}

// SetSize sets the size of the log viewport
func (lv *LogViewer) SetSize(width, height int) {
	borderWidth := 2 // left + right border
	lv.width = width
	lv.height = height

	if !lv.ready {
		lv.viewport = viewport.New(width-borderWidth, height)
		initialContent := lv.logWriter.GetContent()
		wrappedContent := lipgloss.NewStyle().
			Width(lv.viewport.Width).
			Render(initialContent)
		lv.viewport.SetContent(wrappedContent)
		lv.ready = true
	} else {
		lv.viewport.Width = width - borderWidth
		lv.viewport.Height = height
		content := lv.logWriter.GetContent()
		wrappedContent := lipgloss.NewStyle().
			Width(lv.viewport.Width).
			Render(content)
		lv.viewport.SetContent(wrappedContent)
		lv.viewport.GotoBottom()
	}
}

// View renders the log viewer component
func (lv *LogViewer) View() string {
	if !lv.ready {
		return ""
	}

	// Bottom viewport style with slightly lighter background
	bottomStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("235")).
		Width(lv.width - 2).
		Height(lv.height)

	content := lv.viewport.View()
	return bottomStyle.Render(content)
}

type logUpdateMsg struct{}
