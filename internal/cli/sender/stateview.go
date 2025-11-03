package sender

import (
	"sync"
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
func RenderStateView(width int) string {
	state := GetState()

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

	return content
}
