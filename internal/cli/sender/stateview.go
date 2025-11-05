package sender

import (
	"sync"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
)

// SenderState holds the current sender state
type SenderState struct {
	mu      sync.RWMutex
	Status  string // "connecting", "connected", "failed"
	Message string // Optional status message
}

var (
	currentState = &SenderState{
		Status: "connecting",
	}
)

// GetState returns the current sender state
func GetState() *SenderState {
	currentState.mu.RLock()
	defer currentState.mu.RUnlock()
	return &SenderState{
		Status:  currentState.Status,
		Message: currentState.Message,
	}
}

// SetStatus updates the sender status
func SetStatus(status, message string) {
	currentState.mu.Lock()
	defer currentState.mu.Unlock()
	currentState.Status = status
	currentState.Message = message
}

// RenderStateView renders the sender state (connection status) for the right side
func RenderStateView(width int, connectingSp spinner.Model, connectedSp spinner.Model) string {
	state := GetState()

	var content string
	switch state.Status {
	case "connecting":
		spinnerView := connectingSp.View()
		connectingStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("220")). // Yellow shade
			Bold(true)
		content = "\n" + spinnerView + " " + connectingStyle.Render("Connecting...")
		if state.Message != "" {
			messageStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("75")) // Bluish color
			content += "\n" + messageStyle.Render(state.Message)
		}
	case "connected":
		spinnerView := connectedSp.View()
		connectedStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("135")). // Purple shade
			Bold(true)
		content = "\n" + spinnerView + " " + connectedStyle.Render("Connected")
		if state.Message != "" {
			messageStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("75")) // Bluish color
			content += "\n" + messageStyle.Render(state.Message)
		}
	case "failed":
		failedStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("160")). // Red shade
			Bold(true)
		content = "\nStatus: " + failedStyle.Render("Failed")
		if state.Message != "" {
			errorStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("196")) // Bright red
			content += "\nError: " + errorStyle.Render(state.Message)
		}
		content += "\n\nPress 'q' to quit"
	default:
		content = "\nStatus: Unknown"
	}

	return content
}
