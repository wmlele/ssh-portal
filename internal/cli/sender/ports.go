package sender

import (
	"fmt"
	"strings"
	"sync"
	"time"

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

// RenderPortForwardsList renders the list of port forwards for the left side
func RenderPortForwardsList(width int) string {
	forwards := GetAllPortForwards()

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("62")).
		MarginBottom(1)

	title := titleStyle.Render("Port Forwards")

	if len(forwards) == 0 {
		return title + "\n\nNo port forwards configured"
	}

	// Table header
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("240")).
		Padding(0, 1)

	header := headerStyle.Render("Local              Remote")
	divider := strings.Repeat("─", width-4)

	var rows []string
	rows = append(rows, title)
	rows = append(rows, header)
	rows = append(rows, divider)

	rowStyle := lipgloss.NewStyle().Padding(0, 1)
	for _, pf := range forwards {
		listen := pf.Listen
		target := pf.Target

		// Truncate if too long
		if len(listen) > 18 {
			listen = listen[:18]
		}
		if len(target) > 18 {
			target = target[:18]
		}

		row := fmt.Sprintf("%-18s %-18s", listen, target)
		rows = append(rows, rowStyle.Render(row))
	}

	content := strings.Join(rows, "\n")
	return content
}

