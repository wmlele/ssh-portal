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

func startSSHClient(ctx context.Context, relayHost string, relayPort int, code string) error {
	// Build relay TCP address
	relayTCP := net.JoinHostPort(relayHost, strconv.Itoa(relayPort))

	SetStatus("connecting", "Connecting to relay...")

	// Connect and perform protocol handshake
	result, err := ConnectAndHandshake(relayTCP, code)
	if err != nil {
		SetStatus("failed", fmt.Sprintf("Handshake failed: %v", err))
		log.Printf("handshake failed: %v", err)
		return err
	}
	defer result.Conn.Close()

	log.Println("SSH connection prepared", result.SSHConn)

	SetStatus("connecting", "Establishing SSH connection...")

	// Establish SSH connection
	cc, chans, reqs, err := ssh.NewClientConn(result.SSHConn, "paired", result.ClientConfig)
	if err != nil {
		SetStatus("failed", fmt.Sprintf("SSH connection failed: %v", err))
		log.Printf("SSH connection failed: %v", err)
		return err
	}

	log.Println("SSH connection established")

	client := ssh.NewClient(cc, chans, reqs)
	defer client.Close()

	// Store SSH client for dynamic port forward management
	sshClientMu.Lock()
	sshClient = client
	sshClientMu.Unlock()

	SetStatus("connected", "SSH connection established")

	// Monitor connection for closure - check if client operations fail
	go func() {
		// Monitor by periodically checking connection state
		// or by detecting when a client operation fails
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Try to send a keepalive or check connection
				_, _, err := client.SendRequest("keepalive@ssh-portal", false, nil)
				if err != nil {
					log.Printf("Receiver connection closed: %v", err)
					SetStatus("failed", fmt.Sprintf("Connection closed: %v", err))
					// Clear SSH client on connection failure
					sshClientMu.Lock()
					sshClient = nil
					sshClientMu.Unlock()
					return
				}
			}
		}
	}()

	// Wait for context cancellation
	<-ctx.Done()

	// Cleanup: close all active forwards
	sshClientMu.Lock()
	sshClient = nil
	sshClientMu.Unlock()
	closeAllActiveForwards()

	return nil
}

// createLocalForward creates a new local port forward and immediately starts forwarding traffic
func createLocalForward(pfID, listen, target string) error {
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
func Run(relayHost string, relayPort int, code string, interactive bool) error {
	if code == "" {
		return fmt.Errorf("code is required")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if interactive {
		// Start TUI
		if err := startTUI(ctx, cancel); err != nil {
			return fmt.Errorf("failed to start TUI: %w", err)
		}
	}

	// Start SSH client in a goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- startSSHClient(ctx, relayHost, relayPort, code)
	}()

	// Wait for context cancellation or error
	select {
	case <-ctx.Done():
		// TUI or user initiated shutdown
		return nil
	case err := <-errChan:
		// Connection failed
		if err != nil && !interactive {
			// In non-interactive mode, return the error
			return err
		}
		// In interactive mode, wait for user to quit
		<-ctx.Done()
		return nil
	}
}
