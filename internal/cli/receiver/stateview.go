package receiver

import (
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

// RenderStateView renders the receiver state (code/rid/fp) without styling
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
		content = "\nCode: " + state.Code + "\n"
		content += "RID:  " + state.RID + "\n"
		content += "FP:   " + state.FP
	}

	// Join header and content
	return lipgloss.JoinVertical(lipgloss.Left, header, content)
}
