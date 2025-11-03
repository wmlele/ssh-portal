package receiver

// ReceiverState holds the current receiver state
type ReceiverState struct {
	Code string
	RID  string
	FP   string
}

var currentState = &ReceiverState{}

// GetState returns the current receiver state
func GetState() *ReceiverState {
	return currentState
}

// SetState updates the receiver state
func SetState(code, rid, fp string) {
	currentState.Code = code
	currentState.RID = rid
	currentState.FP = fp
}

// RenderStateView renders the receiver state (code/rid/fp) without styling
func RenderStateView(width int) string {
	state := GetState()
	
	if state.Code == "" && state.RID == "" && state.FP == "" {
		return "Waiting for connection..."
	}
	
	content := "Code: " + state.Code + "\n"
	content += "RID:  " + state.RID + "\n"
	content += "FP:   " + state.FP
	
	return content
}

