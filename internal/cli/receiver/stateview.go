package receiver

import (
	"fmt"
	"sync"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
)

// ReceiverState holds the current receiver state
type ReceiverState struct {
	mu    sync.RWMutex
	Code  string
	RID   string
	FP    string
	Error string
}

var currentState = &ReceiverState{}

// GetState returns the current receiver state
func GetState() *ReceiverState {
	currentState.mu.RLock()
	defer currentState.mu.RUnlock()
	return &ReceiverState{
		Code:  currentState.Code,
		RID:   currentState.RID,
		FP:    currentState.FP,
		Error: currentState.Error,
	}
}

// SetState updates the receiver state
func SetState(code, rid, fp string) {
	currentState.mu.Lock()
	defer currentState.mu.Unlock()
	currentState.Code = code
	currentState.RID = rid
	currentState.FP = fp
	currentState.Error = "" // Clear error on successful connection
}

// SetError sets an error message in the state
func SetError(err string) {
	currentState.mu.Lock()
	defer currentState.mu.Unlock()
	currentState.Error = err
}

// NewForwardsTable creates and returns a table.Model configured for DirectTCPIP forwards
func NewForwardsTable(width, height int) table.Model {
	if width < 20 {
		width = 20
	}
	if height < 3 {
		height = 3
	}
	availableWidth := width - 4
	// Two columns: Origin, Destination
	colWidth := availableWidth / 2

	columns := []table.Column{
		{Title: "Origin", Width: colWidth},
		{Title: "Destination", Width: colWidth},
	}

	rows := []table.Row{}
	forwards := GetAllDirectTCPIPs()
	for _, fwd := range forwards {
		origin := fmt.Sprintf("%s:%d", fwd.OriginAddr, fwd.OriginPort)
		dest := fmt.Sprintf("%s:%d", fwd.DestAddr, fwd.DestPort)

		// Truncate if too long
		if len(origin) > colWidth {
			origin = origin[:colWidth]
		}
		if len(dest) > colWidth {
			dest = dest[:colWidth]
		}

		rows = append(rows, table.Row{origin, dest})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(height),
		table.WithWidth(width),
	)

	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(true).
		Foreground(lipgloss.Color("62"))
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("230")).
		Background(lipgloss.Color("62")).
		Bold(false)
	t.SetStyles(s)

	return t
}

// UpdateForwardsTable updates the table with current forwards data
func UpdateForwardsTable(t table.Model, width, height int) table.Model {
	if width < 20 {
		width = 20
	}
	if height < 3 {
		height = 3
	}
	availableWidth := width - 4
	colWidth := availableWidth / 2

	columns := []table.Column{
		{Title: "Origin", Width: colWidth},
		{Title: "Destination", Width: colWidth},
	}

	rows := []table.Row{}
	forwards := GetAllDirectTCPIPs()
	for _, fwd := range forwards {
		origin := fmt.Sprintf("%s:%d", fwd.OriginAddr, fwd.OriginPort)
		dest := fmt.Sprintf("%s:%d", fwd.DestAddr, fwd.DestPort)

		// Truncate if too long
		if len(origin) > colWidth {
			origin = origin[:colWidth]
		}
		if len(dest) > colWidth {
			dest = dest[:colWidth]
		}

		rows = append(rows, table.Row{origin, dest})
	}

	// Preserve current cursor position before updating
	currentCursor := t.Cursor()

	// Update table
	t.SetColumns(columns)
	t.SetRows(rows)
	t.SetWidth(width)
	t.SetHeight(height)

	// Restore cursor position, but clamp it to valid range
	if len(rows) > 0 {
		if currentCursor >= len(rows) {
			currentCursor = len(rows) - 1
		}
		if currentCursor < 0 {
			currentCursor = 0
		}
		t.SetCursor(currentCursor)
	} else {
		// No rows, reset cursor
		t.SetCursor(0)
	}

	return t
}

// RenderLeftPaneContent renders the connection info for the left pane
func RenderLeftPaneContent(width int) string {
	state := GetState()

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("62")).
		MarginBottom(1)

	title := titleStyle.Render("Connection Info")

	var content string
	if state.Error != "" {
		content = "ERROR: " + state.Error + "\n\nPress 'q' or Ctrl+C to quit"
	} else if state.Code == "" && state.RID == "" && state.FP == "" {
		content = "Waiting for connection..."
	} else {
		content = "Code: " + state.Code + "\n"
		content += "RID:  " + state.RID + "\n"
		content += "FP:   " + state.FP
	}

	result := lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		content,
	)

	return result
}

// RenderRightPaneContent renders the forwards table with header for the right pane
func RenderRightPaneContent(width int, forwardsTable table.Model) string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("62")).
		MarginBottom(1)

	infoStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		MarginBottom(1)

	title := titleStyle.Render("Active TCP/IP Forwards")

	forwards := GetAllDirectTCPIPs()
	info := infoStyle.Render(fmt.Sprintf("Active: %d", len(forwards)))

	tableView := forwardsTable.View()
	if tableView == "" {
		tableView = "  No active forwards"
	}

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		info,
		tableView,
	)

	return content
}
