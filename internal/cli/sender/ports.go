package sender

import (
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/huh"
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

// PortForwardForm holds the form data for creating a new port forward
type PortForwardForm struct {
	LocalPort  string
	RemoteAddr string
	RemotePort string
}

// NewPortForwardForm creates a new huh form for adding port forwards
func NewPortForwardForm(width int, formData *PortForwardForm) *huh.Form {
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Local Port").
				Description("Local port to listen on (e.g., 10022)").
				Placeholder("10022").
				Value(&formData.LocalPort).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("local port is required")
					}
					port, err := strconv.Atoi(s)
					if err != nil {
						return fmt.Errorf("invalid port number")
					}
					if port < 1 || port > 65535 {
						return fmt.Errorf("port must be between 1 and 65535")
					}
					return nil
				}),
			huh.NewInput().
				Title("Remote Address").
				Description("Remote address to forward to (e.g., 127.0.0.1)").
				Placeholder("127.0.0.1").
				Value(&formData.RemoteAddr).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("remote address is required")
					}
					if net.ParseIP(s) == nil && s != "localhost" {
						return fmt.Errorf("invalid IP address")
					}
					return nil
				}),
			huh.NewInput().
				Title("Remote Port").
				Description("Remote port to forward to (e.g., 22)").
				Placeholder("22").
				Value(&formData.RemotePort).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("remote port is required")
					}
					port, err := strconv.Atoi(s)
					if err != nil {
						return fmt.Errorf("invalid port number")
					}
					if port < 1 || port > 65535 {
						return fmt.Errorf("port must be between 1 and 65535")
					}
					return nil
				}),
		),
	).WithWidth(width)

	return form
}

// BuildListenAddress constructs the listen address from local port
func BuildListenAddress(localPort string) string {
	return net.JoinHostPort("127.0.0.1", localPort)
}

// BuildTargetAddress constructs the target address from remote address and port
func BuildTargetAddress(remoteAddr, remotePort string) string {
	if remoteAddr == "localhost" {
		remoteAddr = "127.0.0.1"
	}
	return net.JoinHostPort(remoteAddr, remotePort)
}
