package receiver

import (
	"strings"
	"sync"

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

// RenderStateView renders the receiver state (code/rid/fp) without styling
func RenderStateView(width int) string {
	state := GetState()
	
	// Header with software name and colored bar
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("62")).
		Padding(0, 1)
	
	barStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("62")).
		Width(width).
		Height(1)
	
	title := headerStyle.Render("SSH Portal - Receiver")
	bar := barStyle.Render(strings.Repeat(" ", width))
	header := lipgloss.JoinVertical(lipgloss.Left, title, bar)
	
	var content string
	
	// Show error if present
	if state.Error != "" {
		content = "ERROR: " + state.Error + "\n\nPress 'q' or Ctrl+C to quit"
	} else if state.Code == "" && state.RID == "" && state.FP == "" {
		content = "Waiting for connection..."
	} else {
		content = "Code: " + state.Code + "\n"
		content += "RID:  " + state.RID + "\n"
		content += "FP:   " + state.FP
	}
	
	// Join header and content
	return lipgloss.JoinVertical(lipgloss.Left, header, content)
}

