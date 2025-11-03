package receiver

import (
	"fmt"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"

	"ssh-portal/internal/cli/tui"
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

// RenderStateView renders the receiver state (code/rid/fp and TCP/IP forwards) without styling
func RenderStateView(width int) string {
	state := GetState()

	// Header with software name and colored bar
	header := tui.RenderTitleBar("Receiver", width)

	var content string

	// Show error if present
	if state.Error != "" {
		content = "ERROR: " + state.Error + "\n\nPress 'q' or Ctrl+C to quit"
	} else if state.Code == "" && state.RID == "" && state.FP == "" {
		content = "Waiting for connection..."
	} else {
		// Show connection info
		infoContent := "Code: " + state.Code + "\n"
		infoContent += "RID:  " + state.RID + "\n"
		infoContent += "FP:   " + state.FP + "\n"

		// Show TCP/IP forwards table
		forwards := GetAllDirectTCPIPs()
		forwardsTable := formatTCPIPForwards(forwards, width)

		content = infoContent + "\n" + forwardsTable
	}

	// Join header and content
	return lipgloss.JoinVertical(lipgloss.Left, header, content)
}

func formatTCPIPForwards(forwards []*DirectTCPIP, width int) string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("62")).
		MarginBottom(1)

	title := titleStyle.Render("Active TCP/IP Forwards")

	if len(forwards) == 0 {
		return title + "\nNo active forwards"
	}

	// Table header
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("240")).
		Padding(0, 1)

	header := headerStyle.Render("Origin              Destination")
	divider := strings.Repeat("─", width-4)

	var rows []string
	rows = append(rows, title)
	rows = append(rows, header)
	rows = append(rows, divider)

	rowStyle := lipgloss.NewStyle().Padding(0, 1)
	for _, fwd := range forwards {
		origin := fmt.Sprintf("%s:%d", fwd.OriginAddr, fwd.OriginPort)
		dest := fmt.Sprintf("%s:%d", fwd.DestAddr, fwd.DestPort)

		// Truncate if too long
		if len(origin) > 18 {
			origin = origin[:18]
		}
		if len(dest) > 18 {
			dest = dest[:18]
		}

		row := fmt.Sprintf("%-18s %-18s", origin, dest)
		rows = append(rows, rowStyle.Render(row))
	}

	content := strings.Join(rows, "\n")
	return content
}
