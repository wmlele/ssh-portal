package sender

import (
	"fmt"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
)

// PortForward represents a configured port forward
type PortForward struct {
	ID        string
	Listen    string // local listen address (e.g., "127.0.0.1:10022")
	Target    string // remote target address (e.g., "127.0.0.1:22")
	CreatedAt time.Time
}

var (
	portForwardsMu sync.RWMutex
	portForwards   = make(map[string]*PortForward)
)

// RegisterPortForward registers a new port forward
func RegisterPortForward(listen, target string) string {
	portForwardsMu.Lock()
	defer portForwardsMu.Unlock()

	id := fmt.Sprintf("%d", time.Now().UnixNano())
	pf := &PortForward{
		ID:        id,
		Listen:    listen,
		Target:    target,
		CreatedAt: time.Now(),
	}
	portForwards[id] = pf
	return id
}

// UnregisterPortForward removes a port forward
func UnregisterPortForward(id string) {
	portForwardsMu.Lock()
	defer portForwardsMu.Unlock()
	delete(portForwards, id)
}

// GetAllPortForwards returns all configured port forwards
func GetAllPortForwards() []*PortForward {
	portForwardsMu.RLock()
	defer portForwardsMu.RUnlock()

	result := make([]*PortForward, 0, len(portForwards))
	for _, pf := range portForwards {
		result = append(result, pf)
	}
	return result
}

// NewPortsTable creates and returns a table.Model configured for port forwards
func NewPortsTable(width, height int) table.Model {
	// Calculate column widths - leave some space for borders/padding
	// Table needs at least 4 chars per column, so ensure minimum width
	if width < 20 {
		width = 20
	}
	availableWidth := width - 4 // Account for borders
	columnWidth := availableWidth / 2
	if columnWidth < 4 {
		columnWidth = 4
	}

	columns := []table.Column{
		{Title: "Local", Width: columnWidth},
		{Title: "Remote", Width: columnWidth},
	}

	rows := []table.Row{}
	forwards := GetAllPortForwards()
	for _, pf := range forwards {
		rows = append(rows, table.Row{pf.Listen, pf.Target})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(height),
		table.WithWidth(width),
	)

	// Configure styles
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

// UpdatePortsTable updates the table with current port forwards data
func UpdatePortsTable(t table.Model, width, height int) table.Model {
	// Calculate column widths - ensure minimum size
	if width < 20 {
		width = 20
	}
	if height < 3 {
		height = 3
	}
	availableWidth := width - 4 // Account for borders
	columnWidth := availableWidth / 2
	if columnWidth < 4 {
		columnWidth = 4
	}

	columns := []table.Column{
		{Title: "Local", Width: columnWidth},
		{Title: "Remote", Width: columnWidth},
	}

	rows := []table.Row{}
	forwards := GetAllPortForwards()
	for _, pf := range forwards {
		rows = append(rows, table.Row{pf.Listen, pf.Target})
	}

	// Update table while preserving focus state and cursor position
	t.SetColumns(columns)
	t.SetRows(rows)
	t.SetWidth(width)
	t.SetHeight(height)

	return t
}

// RenderLeftPaneContent renders the complete left pane content including header and table
func RenderLeftPaneContent(width int, portsTable table.Model) string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("62")).
		MarginBottom(1)

	infoStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		MarginBottom(1)

	title := titleStyle.Render("Port Forwards")

	// Get port forward statistics
	forwards := GetAllPortForwards()
	info := infoStyle.Render(fmt.Sprintf("Configured: %d", len(forwards)))

	// Get table view
	tableView := portsTable.View()
	if tableView == "" {
		tableView = "  No port forwards configured"
	}

	// Combine all parts
	content := lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		info,
		tableView,
	)

	return content
}
