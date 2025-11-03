package sender

import (
	"bufio"
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

func startSSHClient(relayHost string, relayPort int, code string) {
	// Build relay TCP address
	relayTCP := net.JoinHostPort(relayHost, strconv.Itoa(relayPort))

	// Connect and perform protocol handshake
	result, err := ConnectAndHandshake(relayTCP, code)
	if err != nil {
		log.Fatalf("handshake failed: %v", err)
	}
	defer result.Conn.Close()

	log.Println("SSH connection prepared", result.SSHConn)

	// Establish SSH connection
	cc, chans, reqs, err := ssh.NewClientConn(result.SSHConn, "paired", result.ClientConfig)
	if err != nil {
		log.Fatalf("SSH connection failed: %v", err)
	}

	log.Println("SSH connection established")

	client := ssh.NewClient(cc, chans, reqs)
	defer client.Close()

	// Monitor connection for closure - check if client operations fail
	go func() {
		// Monitor by periodically checking connection state
		// or by detecting when a client operation fails
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			// Try to send a keepalive or check connection
			_, _, err := client.SendRequest("keepalive@ssh-portal", false, nil)
			if err != nil {
				log.Printf("Receiver connection closed: %v", err)
				return
			}
		}
	}()

	// Start local port forwarding
	go localForward(client, "127.0.0.1:10022", "127.0.0.1:22")
	fmt.Println("Press 'q' and Enter to quit...")

	// Wait for 'q' to quit
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "q" || line == "Q" {
			break
		}
		fmt.Println("Press 'q' and Enter to quit...")
	}
	if err := scanner.Err(); err != nil {
		log.Printf("error reading input: %v", err)
	}
}

func localForward(c *ssh.Client, listen, target string) {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		log.Println("listen:", err)
		return
	}
	defer ln.Close()
	log.Println("local forward listening on", listen, "->", target)
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
	startSSHClient(relayHost, relayPort, code)
	return nil
}
