package sender

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
)

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

	SetStatus("connected", "Port forwarding active on 127.0.0.1:10022 -> 127.0.0.1:22")

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
					return
				}
			}
		}
	}()

	// Start local port forwarding
	go func() {
		localForward(ctx, client, "127.0.0.1:10022", "127.0.0.1:22")
	}()

	// Wait for context cancellation
	<-ctx.Done()
	return nil
}

func localForward(ctx context.Context, c *ssh.Client, listen, target string) {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		log.Println("listen:", err)
		return
	}
	defer ln.Close()
	log.Println("local forward listening on", listen, "->", target)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		lc, err := ln.Accept()
		if err != nil {
			return
		}
		go func(lc net.Conn) {
			rc, err := c.Dial("tcp", target)
			if err != nil {
				log.Printf("Failed to dial target (receiver may have closed): %v", err)
				lc.Close()
				return
			}
			go io.Copy(rc, lc)
			go func() {
				_, err := io.Copy(lc, rc)
				if err != nil && err != io.EOF {
					log.Printf("Connection error (receiver may have closed): %v", err)
				}
				lc.Close()
				rc.Close()
			}()
		}(lc)
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
