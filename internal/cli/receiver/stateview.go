package receiver

import (
	"fmt"
	"sync"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"

	"ssh-portal/internal/cli/tui"
)

// ReceiverState holds the current receiver state
type ReceiverState struct {
	mu             sync.RWMutex
	UserCode       string // User-friendly code (generated from RelayCode + LocalSecret)
	RelayCode      string // Code from relay
	LocalSecret    string // Locally generated secret (not displayed)
	RID            string
	FP             string
	SenderAddr     string // Sender address from ready message
	SSHEstablished bool   // Whether SSH connection is established
	Error          string
}

var currentState = &ReceiverState{}

// GetState returns the current receiver state
func GetState() *ReceiverState {
	currentState.mu.RLock()
	defer currentState.mu.RUnlock()
	return &ReceiverState{
		UserCode:       currentState.UserCode,
		RelayCode:      currentState.RelayCode,
		LocalSecret:    currentState.LocalSecret,
		RID:            currentState.RID,
		FP:             currentState.FP,
		SenderAddr:     currentState.SenderAddr,
		SSHEstablished: currentState.SSHEstablished,
		Error:          currentState.Error,
	}
}

// GetCombinedCode returns the user code (user-friendly code)
func GetCombinedCode() string {
	state := GetState()
	return state.UserCode
}

// SetState updates the receiver state with user code, relay code, local secret, rid, and fingerprint
func SetState(userCode, relayCode, localSecret, rid, fp string) {
	currentState.mu.Lock()
	defer currentState.mu.Unlock()
	currentState.UserCode = userCode
	currentState.RelayCode = relayCode
	currentState.LocalSecret = localSecret
	currentState.RID = rid
	currentState.FP = fp
	currentState.SSHEstablished = false // SSH not established yet
	currentState.Error = ""             // Clear error on successful connection
}

// SetSenderAddr stores the sender address from the ready message
func SetSenderAddr(addr string) {
	currentState.mu.Lock()
	defer currentState.mu.Unlock()
	currentState.SenderAddr = addr
}

// SetSSHEstablished marks the SSH connection as established
func SetSSHEstablished() {
	currentState.mu.Lock()
	defer currentState.mu.Unlock()
	currentState.SSHEstablished = true
}

// SetError sets an error message in the state
func SetError(err string) {
	currentState.mu.Lock()
	defer currentState.mu.Unlock()
	currentState.Error = err
}

// NewForwardsTable creates and returns a table.Model configured for DirectTCPIP forwards
func NewForwardsTable(width, height int) table.Model {
	if width < 20 {
		width = 20
	}
	if height < 3 {
		height = 3
	}
	availableWidth := width - 4
	// Three columns: Src Address, Origin, Destination
	colWidth := availableWidth / 3

	columns := []table.Column{
		{Title: "Src Address", Width: colWidth},
		{Title: "Origin", Width: colWidth},
		{Title: "Destination", Width: colWidth},
	}

	rows := []table.Row{}
	forwards := GetAllDirectTCPIPs()
	for _, fwd := range forwards {
		srcAddr := fwd.SrcAddress
		origin := fmt.Sprintf("%s:%d", fwd.OriginAddr, fwd.OriginPort)
		dest := fmt.Sprintf("%s:%d", fwd.DestAddr, fwd.DestPort)

		// Truncate if too long
		if len(srcAddr) > colWidth {
			srcAddr = srcAddr[:colWidth]
		}
		if len(origin) > colWidth {
			origin = origin[:colWidth]
		}
		if len(dest) > colWidth {
			dest = dest[:colWidth]
		}

		rows = append(rows, table.Row{srcAddr, origin, dest})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(height),
		table.WithWidth(width),
	)

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

// UpdateForwardsTable updates the table with current forwards data
func UpdateForwardsTable(t table.Model, width, height int) table.Model {
	if width < 20 {
		width = 20
	}
	if height < 3 {
		height = 3
	}
	availableWidth := width - 4
	// Three columns: Src Address, Origin, Destination
	colWidth := availableWidth / 3

	columns := []table.Column{
		{Title: "Src Address", Width: colWidth},
		{Title: "Origin", Width: colWidth},
		{Title: "Destination", Width: colWidth},
	}

	rows := []table.Row{}
	forwards := GetAllDirectTCPIPs()
	for _, fwd := range forwards {
		srcAddr := fwd.SrcAddress
		origin := fmt.Sprintf("%s:%d", fwd.OriginAddr, fwd.OriginPort)
		dest := fmt.Sprintf("%s:%d", fwd.DestAddr, fwd.DestPort)

		// Truncate if too long
		if len(srcAddr) > colWidth {
			srcAddr = srcAddr[:colWidth]
		}
		if len(origin) > colWidth {
			origin = origin[:colWidth]
		}
		if len(dest) > colWidth {
			dest = dest[:colWidth]
		}

		rows = append(rows, table.Row{srcAddr, origin, dest})
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

// RenderLeftPaneContent renders the connection info for the left pane
func RenderLeftPaneContent(width int, sp spinner.Model) string {
	state := GetState()

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("62")).
		MarginBottom(1)

	codeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("220")). // Yellow/gold accent color
		Bold(true)

	title := titleStyle.Render("Connection Info")

	var content string
	if state.Error != "" {
		content = "ERROR: " + state.Error + "\n\nPress 'q' or Ctrl+C to quit"
	} else if state.UserCode == "" && state.RID == "" && state.FP == "" {
		spinnerView := sp.View()
		content = "Waiting for connection...\n\n" + spinnerView
	} else {
		content = "Code:      " + codeStyle.Render(state.UserCode) + "\n"
		content += "RelayCode: " + state.RelayCode + "\n"
		content += "RID:       " + state.RID + "\n"
		content += "FP:        " + state.FP
		if !state.SSHEstablished {
			spinnerView := sp.View()
			content += "\n\n" + spinnerView + " Waiting for SSH..."
		} else if state.SenderAddr != "" {
			content += "\n\nConnected to: " + state.SenderAddr
		}
	}

	result := lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		content,
	)

	return result
}

// RenderRightPaneContent renders the forwards table with header for the right pane
func RenderRightPaneContent(width int, forwardsTable table.Model) string {
	forwards := GetAllDirectTCPIPs()
	count := len(forwards)

	// R (Remote/Sender) -> L (Local/Receiver)
	header := tui.RenderDirectionalHeader("R", "21", "L", "62", fmt.Sprintf("%d active", count))

	tableView := forwardsTable.View()
	if tableView == "" {
		tableView = "  No active forwards"
	}

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		tableView,
	)

	return content
}

// NewReverseForwardsTable creates and returns a table.Model for reverse (tcpip-forward) entries
func NewReverseForwardsTable(width, height int) table.Model {
	if width < 20 {
		width = 20
	}
	if height < 3 {
		height = 3
	}
	availableWidth := width - 4
	// Two columns: Listen, Origin
	colWidth := availableWidth / 2

	columns := []table.Column{
		{Title: "Listen", Width: colWidth},
		{Title: "Origin", Width: colWidth},
	}

	rows := []table.Row{}
	revs := GetAllReverseTCPIPs()
	for _, r := range revs {
		listen := fmt.Sprintf("%s:%d", r.ListenAddr, r.ListenPort)
		origin := ""
		if r.OriginAddr != "" {
			origin = fmt.Sprintf("%s:%d", r.OriginAddr, r.OriginPort)
		}
		if len(listen) > colWidth {
			listen = listen[:colWidth]
		}
		if len(origin) > colWidth {
			origin = origin[:colWidth]
		}
		rows = append(rows, table.Row{listen, origin})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(height),
		table.WithWidth(width),
	)

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

// UpdateReverseForwardsTable updates the reverse table with current data
func UpdateReverseForwardsTable(t table.Model, width, height int) table.Model {
	if width < 20 {
		width = 20
	}
	if height < 3 {
		height = 3
	}
	availableWidth := width - 4
	colWidth := availableWidth / 2

	columns := []table.Column{
		{Title: "Listen", Width: colWidth},
		{Title: "Origin", Width: colWidth},
	}

	rows := []table.Row{}
	revs := GetAllReverseTCPIPs()
	for _, r := range revs {
		listen := fmt.Sprintf("%s:%d", r.ListenAddr, r.ListenPort)
		origin := ""
		if r.OriginAddr != "" {
			origin = fmt.Sprintf("%s:%d", r.OriginAddr, r.OriginPort)
		}
		if len(listen) > colWidth {
			listen = listen[:colWidth]
		}
		if len(origin) > colWidth {
			origin = origin[:colWidth]
		}
		rows = append(rows, table.Row{listen, origin})
	}

	currentCursor := t.Cursor()
	t.SetColumns(columns)
	t.SetRows(rows)
	t.SetWidth(width)
	t.SetHeight(height)

	if len(rows) > 0 {
		if currentCursor >= len(rows) {
			currentCursor = len(rows) - 1
		}
		if currentCursor < 0 {
			currentCursor = 0
		}
		t.SetCursor(currentCursor)
	} else {
		t.SetCursor(0)
	}

	return t
}
