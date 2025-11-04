package sender

import (
	"fmt"
	"log"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"ssh-portal/internal/cli/tui"
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

// RegisterPortForward registers a new port forward and immediately starts forwarding
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

	// Start the actual port forward immediately
	if err := createLocalForward(id, listen, target); err != nil {
		log.Printf("Failed to create port forward %s -> %s: %v", listen, target, err)
		// Remove from registry if forward creation failed
		delete(portForwards, id)
		return ""
	}

	return id
}

// UnregisterPortForward removes a port forward and immediately stops forwarding
func UnregisterPortForward(id string) {
	portForwardsMu.Lock()
	_, exists := portForwards[id]
	delete(portForwards, id)
	portForwardsMu.Unlock()

	// Stop the actual port forward immediately
	if exists {
		if err := deleteLocalForward(id); err != nil {
			log.Printf("Failed to delete port forward %s: %v", id, err)
		}
	}
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

func clampTableSize(width, height int) (int, int, int) {
	// Ensure minimum width and height; compute column widths
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
	return width, height, columnWidth
}

func buildPortsColumns(columnWidth int) []table.Column {
	return []table.Column{
		{Title: "Local", Width: columnWidth},
		{Title: "Remote", Width: columnWidth},
	}
}

func buildPortsRows() []table.Row {
	forwards := GetAllPortForwards()

	// Sort by source port (extracted from Listen address)
	sort.Slice(forwards, func(i, j int) bool {
		// Extract port from Listen address (format: "host:port")
		portI := extractPort(forwards[i].Listen)
		portJ := extractPort(forwards[j].Listen)
		return portI < portJ
	})

	rows := make([]table.Row, len(forwards))
	for i, pf := range forwards {
		rows[i] = table.Row{pf.Listen, pf.Target}
	}
	return rows
}

func extractPort(addr string) int {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0
	}
	return port
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
	// Compute sizes and columns
	width, height, columnWidth := clampTableSize(width, height)

	columns := buildPortsColumns(columnWidth)
	rows := buildPortsRows()

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

	// Ensure table remains focused
	t.Focus()

	return t
}

// RenderLeftPaneContent renders the complete left pane content including headers and both tables
func RenderLeftPaneContent(width int, portsTable table.Model, reversePortsTable table.Model, helpModel help.Model) string {
	// Get port forward statistics
	forwards := GetAllPortForwards()
	directCount := len(forwards)
	revs := GetAllReverseForwards()
	reverseCount := len(revs)

	// L (Local) -> R (Remote) for forward port forwards
	headerDirect := tui.RenderDirectionalHeader("L", "62", "R", "21", fmt.Sprintf("%d active", directCount))

	// R (Remote) -> L (Local) for reverse port forwards
	headerReverse := tui.RenderDirectionalHeader("R", "21", "L", "62", fmt.Sprintf("%d active", reverseCount))

	// Get table views
	directView := portsTable.View()
	if directView == "" {
		directView = "  No port forwards configured"
	}
	reverseView := reversePortsTable.View()
	if reverseView == "" {
		reverseView = "  No reverse forwards configured"
	}

	// Create help key bindings
	keys := []key.Binding{
		key.NewBinding(
			key.WithKeys("l"),
			key.WithHelp("l", "new port forward"),
		),
		key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "new reverse forward"),
		),
		key.NewBinding(
			key.WithKeys("d"),
			key.WithHelp("d", "delete port forward"),
		),
		key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "switch table"),
		),
		key.NewBinding(
			key.WithKeys("up", "down"),
			key.WithHelp("↑/↓", "navigate"),
		),
	}

	// Render help view
	helpView := helpModel.ShortHelpView(keys)

	// Combine all parts
	content := lipgloss.JoinVertical(
		lipgloss.Left,
		headerDirect,
		directView,
		headerReverse,
		reverseView,
		"",
		helpView,
	)

	return content
}

// PortForwardForm holds the form data for creating a new port forward
type PortForwardForm struct {
	LocalAddr  string
	LocalPort  string
	RemoteAddr string
	RemotePort string
}

// NewPortForwardForm creates a new huh form for adding port forwards
func NewPortForwardForm(width int, formData *PortForwardForm) *huh.Form {
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Local Address").
				Description("Local address to listen on (e.g., 0.0.0.0 or 127.0.0.1). Leave empty for 0.0.0.0").
				Placeholder("0.0.0.0").
				Value(&formData.LocalAddr).
				Validate(func(s string) error {
					if s == "" {
						return nil // Empty is allowed, defaults to 0.0.0.0
					}
					if net.ParseIP(s) == nil && s != "localhost" {
						return fmt.Errorf("invalid IP address")
					}
					return nil
				}),
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

// BuildListenAddress constructs the listen address from local address and port
func BuildListenAddress(localAddr, localPort string) string {
	if localAddr == "" {
		localAddr = "0.0.0.0"
	}
	if localAddr == "localhost" {
		localAddr = "127.0.0.1"
	}
	return net.JoinHostPort(localAddr, localPort)
}

// BuildTargetAddress constructs the target address from remote address and port
func BuildTargetAddress(remoteAddr, remotePort string) string {
	if remoteAddr == "localhost" {
		remoteAddr = "127.0.0.1"
	}
	return net.JoinHostPort(remoteAddr, remotePort)
}
