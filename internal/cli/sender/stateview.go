package sender

import (
	"sync"

	"github.com/charmbracelet/lipgloss"

	"ssh-portal/internal/cli/tui"
)

// SenderState holds the current sender state
type SenderState struct {
	mu      sync.RWMutex
	Status  string // "connecting", "connected", "failed"
	Message string // Optional status message
}

var currentState = &SenderState{
	Status: "connecting",
}

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

// RenderStateView renders the sender state (connection status)
func RenderStateView(width int) string {
	state := GetState()

	// Header with software name and colored bar
	header := tui.RenderTitleBar("Sender", width)

	var content string
	switch state.Status {
	case "connecting":
		content = "\nStatus: Connecting..."
		if state.Message != "" {
			content += "\n" + state.Message
		}
	case "connected":
		content = "\nStatus: Connected"
		if state.Message != "" {
			content += "\n" + state.Message
		}
	case "failed":
		content = "\nStatus: Failed"
		if state.Message != "" {
			content += "\nError: " + state.Message
		}
		content += "\n\nPress 'q' or Ctrl+C to quit"
	default:
		content = "\nStatus: Unknown"
	}

	// Join header and content
	return lipgloss.JoinVertical(lipgloss.Left, header, content)
}

