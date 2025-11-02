package sender

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"

	"golang.org/x/crypto/ssh"
)

const relayTCP = "127.0.0.1:4430" // set me

// --- Main client ---

func startSSHClient(code string) {
	// Connect and perform protocol handshake
	result, err := ConnectAndHandshake(relayTCP, code)
	if err != nil {
		log.Fatalf("handshake failed: %v", err)
	}
	defer result.Conn.Close()

	log.Println("SSH connection prepared", result.SSHConn)

	// Note: Don't read from result.Conn directly - it's the same underlying socket
	// that result.SSHConn uses. The buffered data was already extracted in prepareSSHConnection
	// and will be shown in debug output from SyncToBannerReader when SSH handshake starts.

	// Establish SSH connection
	cc, chans, reqs, err := ssh.NewClientConn(result.SSHConn, "paired", result.ClientConfig)
	if err != nil {
		log.Fatalf("SSH connection failed: %v", err)
	}

	log.Println("SSH connection established")

	client := ssh.NewClient(cc, chans, reqs)
	defer client.Close()

	// Start local port forwarding
	go localForward(client, "127.0.0.1:10022", "127.0.0.1:22")
	fmt.Println("try: ssh -p 10022 localhost")

	// Start interactive shell
	if err := interactiveShell(client); err != nil {
		log.Fatalf("shell error: %v", err)
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
				lc.Close()
				return
			}
			go io.Copy(rc, lc)
			go func() { io.Copy(lc, rc); lc.Close(); rc.Close() }()
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
func Run(code string) error {
	if code == "" {
		return fmt.Errorf("code is required")
	}
	startSSHClient(code)
	return nil
}
