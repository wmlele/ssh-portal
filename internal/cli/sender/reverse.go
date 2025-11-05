package sender

import (
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"ssh-portal/internal/cli/validate"
)

// ReverseForward represents a remote (ssh -R) port forward requested by the sender.
// The receiver listens on BindAddr:BindPort and connections are forwarded back to the sender,
// which then connects to LocalTarget and bridges traffic.
type ReverseForward struct {
	ID          string
	BindAddr    string
	BindPort    uint32 // actual bound port on receiver (may be allocated if 0 requested)
	LocalTarget string // local target address on sender side (e.g., 127.0.0.1:22)
	Listener    net.Listener
	CreatedAt   time.Time
}

var (
	reverseFwdsMu sync.RWMutex
	reverseFwds   = make(map[string]*ReverseForward)
)

// StartReverseForward requests a remote port forward on the receiver (ssh -R style).
// bindAddr is the address on receiver to bind (e.g., 0.0.0.0 or 127.0.0.1), bindPort can be 0 to auto-assign.
// localTarget is the address on the sender machine to forward connections to (e.g., 127.0.0.1:22).
// Returns the forward ID and the actual bound port.
func StartReverseForward(bindAddr string, bindPort uint32, localTarget string) (string, uint32, error) {
	// Validate bindAddr (host only, port is separate)
	if err := validate.ValidateHost(bindAddr); err != nil {
		return "", 0, fmt.Errorf("invalid bind address: %w", err)
	}

	// Validate bindPort (0 is allowed for auto-assign)
	if bindPort > 65535 {
		return "", 0, fmt.Errorf("bind port must be between 0 and 65535, got %d", bindPort)
	}

	// Validate localTarget (full address)
	if err := validate.ValidateAddress(localTarget, "local target"); err != nil {
		return "", 0, err
	}

	sshClientMu.RLock()
	client := sshClient
	sshClientMu.RUnlock()
	if client == nil {
		return "", 0, fmt.Errorf("SSH client not connected")
	}

	remote := net.JoinHostPort(bindAddr, strconv.FormatUint(uint64(bindPort), 10))
	ln, err := client.Listen("tcp", remote)
	if err != nil {
		return "", 0, fmt.Errorf("remote listen failed on %s: %w", remote, err)
	}

	// Determine the actual bound port (useful if 0 requested)
	actualPort := uint32(0)
	if addr, ok := ln.Addr().(*net.TCPAddr); ok {
		actualPort = uint32(addr.Port)
	}

	id := fmt.Sprintf("%d", time.Now().UnixNano())
	rf := &ReverseForward{
		ID:          id,
		BindAddr:    bindAddr,
		BindPort:    actualPort,
		LocalTarget: localTarget,
		Listener:    ln,
		CreatedAt:   time.Now(),
	}

	reverseFwdsMu.Lock()
	reverseFwds[id] = rf
	reverseFwdsMu.Unlock()

	// Accept loop: for each remote connection, connect to local target and bridge
	go func(id string, l net.Listener, target string) {
		for {
			rc, err := l.Accept()
			if err != nil {
				// Listener closed or error
				log.Printf("[SENDER R-FWD] listener closed id=%s: %v", id, err)
				return
			}
			go func(remoteConn net.Conn) {
				defer remoteConn.Close()
				lc, err := net.Dial("tcp", target)
				if err != nil {
					log.Printf("[SENDER R-FWD] dial local target %s failed: %v", target, err)
					return
				}
				defer lc.Close()
				// Bridge both directions
				// Copy from remote to local in background
				errChan := make(chan error, 1)
				go func() {
					_, err := io.Copy(lc, remoteConn)
					errChan <- err
				}()
				// Copy from local to remote (blocking)
				_, err = io.Copy(remoteConn, lc)
				// Wait for the other direction and log errors (EOF is expected/normal)
				if err2 := <-errChan; err2 != nil && err2 != io.EOF {
					log.Printf("[SENDER R-FWD] copy error (remote->local): %v", err2)
				}
				if err != nil && err != io.EOF {
					log.Printf("[SENDER R-FWD] copy error (local->remote): %v", err)
				}
			}(rc)
		}
	}(id, ln, localTarget)

	return id, actualPort, nil
}

// StopReverseForward cancels a previously started remote forward by ID.
func StopReverseForward(id string) error {
	reverseFwdsMu.Lock()
	rf, ok := reverseFwds[id]
	if ok {
		delete(reverseFwds, id)
	}
	reverseFwdsMu.Unlock()
	if !ok {
		return fmt.Errorf("reverse forward %s not found", id)
	}
	if rf.Listener != nil {
		if err := rf.Listener.Close(); err != nil {
			return fmt.Errorf("close listener: %w", err)
		}
	}
	return nil
}

// GetAllReverseForwards returns all active reverse forwards.
func GetAllReverseForwards() []*ReverseForward {
	reverseFwdsMu.RLock()
	defer reverseFwdsMu.RUnlock()
	result := make([]*ReverseForward, 0, len(reverseFwds))
	for _, rf := range reverseFwds {
		result = append(result, rf)
	}
	return result
}

// ReverseForwardForm holds the form data for creating a new reverse port forward (ssh -R)
type ReverseForwardForm struct {
	RemoteAddr string // receiver bind address (e.g., 0.0.0.0)
	RemotePort string // receiver bind port (e.g., 0 or 2200)
	LocalAddr  string // local target address on sender (e.g., 127.0.0.1)
	LocalPort  string // local target port on sender (e.g., 22)
}

// BuildLocalTarget constructs the local target address from address and port
func BuildLocalTarget(localAddr, localPort string) string {
	if localAddr == "localhost" {
		localAddr = "127.0.0.1"
	}
	return net.JoinHostPort(localAddr, localPort)
}

// NewReverseForwardForm creates a new huh form for adding reverse port forwards
func NewReverseForwardForm(width int, formData *ReverseForwardForm) *huh.Form {
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Remote Listen Address").
				Description("Receiver address to bind (e.g., 0.0.0.0 or 127.0.0.1). Leave empty for 0.0.0.0").
				Placeholder("0.0.0.0").
				Value(&formData.RemoteAddr).
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
				Title("Remote Listen Port").
				Description("Receiver port to bind (e.g., 0 or 2200)").
				Placeholder("0").
				Value(&formData.RemotePort).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("remote port is required")
					}
					port, err := strconv.Atoi(s)
					if err != nil {
						return fmt.Errorf("invalid port number")
					}
					if port < 0 || port > 65535 {
						return fmt.Errorf("port must be between 0 and 65535")
					}
					return nil
				}),
			huh.NewInput().
				Title("Local Target Address").
				Description("Local address on this machine to forward to (e.g., 127.0.0.1)").
				Placeholder("127.0.0.1").
				Value(&formData.LocalAddr).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("local address is required")
					}
					if net.ParseIP(s) == nil && s != "localhost" {
						return fmt.Errorf("invalid IP address")
					}
					return nil
				}),
			huh.NewInput().
				Title("Local Target Port").
				Description("Local port on this machine to forward to (e.g., 22)").
				Placeholder("22").
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
		),
	).WithWidth(width)

	return form
}

// NewReverseForwardsTable creates and returns a table.Model for reverse forwards
func NewReverseForwardsTable(width, height int) table.Model {
	if width < 20 {
		width = 20
	}
	availableWidth := width - 4
	columnWidth := availableWidth / 2
	if columnWidth < 4 {
		columnWidth = 4
	}

	columns := []table.Column{
		{Title: "Remote Listen", Width: columnWidth},
		{Title: "Local Target", Width: columnWidth},
	}

	rows := []table.Row{}
	revs := GetAllReverseForwards()
	for _, rf := range revs {
		remoteListen := fmt.Sprintf("%s:%d", rf.BindAddr, rf.BindPort)
		rows = append(rows, table.Row{remoteListen, rf.LocalTarget})
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

// UpdateReverseForwardsTable updates the reverse forwards table with current data
func UpdateReverseForwardsTable(t table.Model, width, height int, isActive bool) table.Model {
	width, height, columnWidth := clampTableSize(width, height)

	columns := []table.Column{
		{Title: "Remote Listen", Width: columnWidth},
		{Title: "Local Target", Width: columnWidth},
	}

	rows := []table.Row{}
	revs := GetAllReverseForwards()
	for _, rf := range revs {
		remoteListen := fmt.Sprintf("%s:%d", rf.BindAddr, rf.BindPort)
		rows = append(rows, table.Row{remoteListen, rf.LocalTarget})
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

	// Update header border color based on active state
	s := table.DefaultStyles()
	// Default border color (subtle gray)
	defaultBorderColor := lipgloss.Color("240")
	// Focused border color (bright blue/purple - more visible)
	focusedBorderColor := lipgloss.Color("135") // Brighter purple/blue
	
	borderColor := defaultBorderColor
	if isActive {
		borderColor = focusedBorderColor
	}
	
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(borderColor).
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
