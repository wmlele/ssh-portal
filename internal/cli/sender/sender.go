package sender

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"ssh-portal/internal/cli/validate"
	"ssh-portal/internal/version"
)

var (
	sshClientMu    sync.RWMutex
	sshClient      *ssh.Client
	activeForwards = make(map[string]*activeForward) // key is port forward ID
	forwardsMu     sync.RWMutex
)

type activeForward struct {
	listener net.Listener
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
}

// --- Main client ---

func startSSHClient(ctx context.Context, relayHost string, relayPort int, code string, keepaliveTimeout time.Duration, identity string, token string) error {
	// Build relay TCP address
	relayTCP := net.JoinHostPort(relayHost, strconv.Itoa(relayPort))

	log.Printf("Connecting to relay: %s", relayTCP)
	SetStatus("connecting", "Connecting to relay...")

	// Connect and perform protocol handshake
	// Provide hello metadata: keepalive seconds and optional identity
	result, err := ConnectAndHandshake(relayTCP, code, int(keepaliveTimeout/time.Second), identity, token)
	if err != nil {
		SetStatus("failed", fmt.Sprintf("Handshake failed: %v", err))
		log.Printf("handshake failed: %v", err)
		return err
	}
	defer result.Conn.Close()

	log.Printf("Connected to relay: %s", relayTCP)
	SetStatus("connecting", "Establishing SSH connection...")

	// Establish SSH connection
	cc, chans, reqs, err := ssh.NewClientConn(result.SSHConn, "paired", result.ClientConfig)
	if err != nil {
		SetStatus("failed", fmt.Sprintf("SSH connection failed: %v", err))
		log.Printf("SSH connection failed: %v", err)
		return err
	}

	log.Printf("SSH connection established with receiver via relay: %s", relayTCP)

	client := ssh.NewClient(cc, chans, reqs)

	// Store SSH client for dynamic port forward management
	sshClientMu.Lock()
	sshClient = client
	sshClientMu.Unlock()

	SetStatus("connected", "SSH connection established")

	// Monitor connection for closure and send keepalives
	keepaliveInterval := 5 * time.Second
	lastKeepalive := time.Now()
	go func() {
		ticker := time.NewTicker(keepaliveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Send keepalive request
				ok, _, err := client.SendRequest("keepalive@ssh-portal", true, nil)
				if err != nil || !ok {
					log.Printf("Keepalive failed, connection closed: %v", err)
					SetStatus("failed", fmt.Sprintf("Connection closed: %v", err))
					// Clear SSH client on connection failure
					sshClientMu.Lock()
					sshClient = nil
					sshClientMu.Unlock()
					closeAllActiveForwards()
					closeAllReverseForwards()
					client.Close()
					return
				}
				lastKeepalive = time.Now()
			}
		}
	}()

	// Monitor for missed keepalives (connection health check)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if time.Since(lastKeepalive) > keepaliveTimeout {
					log.Printf("Keepalive timeout, connection appears dead")
					SetStatus("failed", "Connection timeout")
					// Clear SSH client
					sshClientMu.Lock()
					sshClient = nil
					sshClientMu.Unlock()
					closeAllActiveForwards()
					closeAllReverseForwards()
					client.Close()
					return
				}
			}
		}
	}()

	// Wait for context cancellation
	<-ctx.Done()

	// Cleanup: close all active forwards and reverse forwards
	log.Printf("Shutting down sender, closing all connections...")
	sshClientMu.Lock()
	clientToClose := sshClient
	sshClient = nil
	sshClientMu.Unlock()

	closeAllActiveForwards()
	closeAllReverseForwards()

	if clientToClose != nil {
		clientToClose.Close()
	}

	return nil
}

// createLocalForward creates a new local port forward and immediately starts forwarding traffic
func createLocalForward(pfID, listen, target string) error {
	// Validate pfID
	if pfID == "" {
		return fmt.Errorf("port forward ID cannot be empty")
	}
	if len(pfID) > 256 {
		return fmt.Errorf("port forward ID too long (max 256 characters)")
	}

	// Validate listen address
	if err := validate.ValidateAddress(listen, "listen"); err != nil {
		return err
	}

	// Validate target address
	if err := validate.ValidateAddress(target, "target"); err != nil {
		return err
	}

	sshClientMu.RLock()
	client := sshClient
	sshClientMu.RUnlock()

	if client == nil {
		return fmt.Errorf("SSH client not connected")
	}

	// Check if forward already exists
	forwardsMu.Lock()
	if _, exists := activeForwards[pfID]; exists {
		forwardsMu.Unlock()
		return fmt.Errorf("port forward %s already exists", pfID)
	}
	forwardsMu.Unlock()

	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", listen, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	forward := &activeForward{
		listener: ln,
		ctx:      ctx,
		cancel:   cancel,
		done:     done,
	}

	forwardsMu.Lock()
	activeForwards[pfID] = forward
	forwardsMu.Unlock()

	log.Printf("Local forward created: %s -> %s", listen, target)

	// Start forwarding in a goroutine
	go func() {
		defer close(done)
		defer ln.Close()

		for {
			select {
			case <-ctx.Done():
				return
			default:
				lc, err := ln.Accept()
				if err != nil {
					// Listener closed
					return
				}

				go func(lc net.Conn) {
					defer lc.Close()
					rc, err := client.Dial("tcp", target)
					if err != nil {
						log.Printf("Failed to dial target %s: %v", target, err)
						return
					}
					defer rc.Close()

					// Bidirectional copy
					go io.Copy(rc, lc)
					_, err = io.Copy(lc, rc)
					if err != nil && err != io.EOF {
						log.Printf("Connection error: %v", err)
					}
				}(lc)
			}
		}
	}()

	return nil
}

// deleteLocalForward stops and removes a local port forward
func deleteLocalForward(pfID string) error {
	forwardsMu.Lock()
	forward, exists := activeForwards[pfID]
	if !exists {
		forwardsMu.Unlock()
		return fmt.Errorf("port forward %s not found", pfID)
	}
	delete(activeForwards, pfID)
	forwardsMu.Unlock()

	// Cancel the context to stop accepting new connections
	forward.cancel()

	// Close the listener
	if forward.listener != nil {
		forward.listener.Close()
	}

	// Wait for the forward to finish (with timeout)
	select {
	case <-forward.done:
		log.Printf("Port forward %s stopped", pfID)
	case <-time.After(5 * time.Second):
		log.Printf("Port forward %s stop timeout", pfID)
	}

	return nil
}

// closeAllActiveForwards closes all active port forwards (used on disconnect)
func closeAllActiveForwards() {
	forwardsMu.Lock()
	forwards := make([]*activeForward, 0, len(activeForwards))
	for id, forward := range activeForwards {
		forwards = append(forwards, forward)
		delete(activeForwards, id)
	}
	forwardsMu.Unlock()

	for _, forward := range forwards {
		forward.cancel()
		if forward.listener != nil {
			forward.listener.Close()
		}
	}
}

// closeAllReverseForwards closes all active reverse port forwards (used on disconnect)
func closeAllReverseForwards() {
	reverseFwdsMu.Lock()
	reverseForwards := make([]*ReverseForward, 0, len(reverseFwds))
	for id, rf := range reverseFwds {
		reverseForwards = append(reverseForwards, rf)
		delete(reverseFwds, id)
	}
	reverseFwdsMu.Unlock()

	for _, rf := range reverseForwards {
		if rf.Listener != nil {
			rf.Listener.Close()
		}
	}
}

func interactiveShell(c *ssh.Client) error {
	sess, err := c.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	_ = sess.RequestPty("xterm-256color", 40, 120, ssh.TerminalModes{})
	sess.Stdin = os.Stdin
	sess.Stdout = os.Stdout
	sess.Stderr = os.Stderr
	return sess.Shell()
}

// Run executes the sender command
func Run(relayHost string, relayPort int, code string, interactive bool, keepaliveTimeout time.Duration, identity string, token string) error {
	return RunWithConfig(relayHost, relayPort, code, interactive, keepaliveTimeout, identity, token, nil)
}

// RunWithConfig executes the sender command with configuration
func RunWithConfig(relayHost string, relayPort int, code string, interactive bool, keepaliveTimeout time.Duration, identity string, token string, cfg *Config) error {
	log.Printf("Starting sender version %s", version.String())
	if code == "" {
		return fmt.Errorf("code is required")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var tuiDone <-chan struct{}
	if interactive {
		// Start TUI
		var err error
		tuiDone, err = startTUI(ctx, cancel)
		if err != nil {
			return fmt.Errorf("failed to start TUI: %w", err)
		}
	}

	// Start SSH client in a goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- startSSHClient(ctx, relayHost, relayPort, code, keepaliveTimeout, identity, token)
	}()

	// Apply port forwards from config after SSH connection is established
	if cfg != nil && (len(cfg.Local) > 0 || len(cfg.Remote) > 0) {
		go applyConfigPortForwards(ctx, cfg)
	}

	// Wait for context cancellation or error
	select {
	case <-ctx.Done():
		// TUI or user initiated shutdown
		// If TUI was running, wait for it to finish cleaning up the terminal
		if tuiDone != nil {
			<-tuiDone
		}
		return nil
	case err := <-errChan:
		// Connection failed
		if err != nil && !interactive {
			// In non-interactive mode, return the error
			return err
		}
		// In interactive mode, wait for user to quit
		<-ctx.Done()
		// If TUI was running, wait for it to finish cleaning up the terminal
		if tuiDone != nil {
			<-tuiDone
		}
		return nil
	}
}

// applyConfigPortForwards applies port forwards from configuration once SSH is connected
func applyConfigPortForwards(ctx context.Context, cfg *Config) {
	// Wait for SSH connection to be established (poll until client is available)
	maxWait := 30 * time.Second
	checkInterval := 500 * time.Millisecond
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()
	timeout := time.After(maxWait)

	for {
		select {
		case <-ctx.Done():
			return
		case <-timeout:
			log.Printf("SSH client not available after waiting, skipping config port forwards")
			return
		case <-ticker.C:
			sshClientMu.RLock()
			client := sshClient
			sshClientMu.RUnlock()
			if client != nil {
				// SSH client is available, proceed to apply port forwards
				applyPortForwards(cfg)
				return
			}
		}
	}
}

// applyPortForwards applies the port forwards from config
func applyPortForwards(cfg *Config) {

	// Apply local port forwards
	for _, pf := range cfg.Local {
		if pf.Bind != "" && pf.Target != "" {
			id := RegisterPortForward(pf.Bind, pf.Target)
			if id != "" {
				log.Printf("Applied config local forward: %s -> %s", pf.Bind, pf.Target)
			} else {
				log.Printf("Failed to apply config local forward: %s -> %s", pf.Bind, pf.Target)
			}
		}
	}

	// Apply remote port forwards
	for _, pf := range cfg.Remote {
		if pf.Bind != "" && pf.Target != "" {
			// Parse bind address (format: "host:port")
			host, portStr, err := net.SplitHostPort(pf.Bind)
			if err != nil {
				log.Printf("Invalid bind address in config: %s: %v", pf.Bind, err)
				continue
			}
			port, err := strconv.ParseUint(portStr, 10, 32)
			if err != nil {
				log.Printf("Invalid port in bind address: %s: %v", pf.Bind, err)
				continue
			}
			// Default host to 0.0.0.0 if empty
			if host == "" {
				host = "0.0.0.0"
			}
			id, actualPort, err := StartReverseForward(host, uint32(port), pf.Target)
			if err != nil {
				log.Printf("Failed to apply config remote forward: %s -> %s: %v", pf.Bind, pf.Target, err)
				continue
			}
			log.Printf("Applied config remote forward: %s -> %s (bound on port %d)", pf.Bind, pf.Target, actualPort)
			_ = id // id is used internally
		}
	}
}
