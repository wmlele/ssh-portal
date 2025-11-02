package sender

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"os"

	"golang.org/x/crypto/ssh"
)

const relayTCP = "127.0.0.1:4430" // adjust

func startSSHClient(code string) {
	// 1) connect + HELLO
	sock, err := net.Dial("tcp", relayTCP)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(sock, "HELLO sender code=%s\n", code)

	// 2) read fp from relay
	br := bufio.NewReader(sock)
	line, err := br.ReadString('\n')
	if err != nil || len(line) < 4 || line[:2] != "OK"[:2] {
		log.Fatalf("relay rejected: %q", line)
	}
	var fp string
	_, _ = fmt.Sscanf(line, "OK fp=%s", &fp)
	fmt.Println("Pinned receiver fp:", fp)

	// 3) SSH client handshake, pinned to fp
	cfg := &ssh.ClientConfig{
		User: "ignored",
		Auth: []ssh.AuthMethod{ssh.Password("ignored")},
		HostKeyCallback: func(host string, addr net.Addr, key ssh.PublicKey) error {
			got := ssh.FingerprintSHA256(key)
			if got != fp {
				return fmt.Errorf("host key mismatch: %s != %s", got, fp)
			}
			return nil
		},
	}
	cc, chans, reqs, err := ssh.NewClientConn(sock, "paired", cfg)
	if err != nil {
		log.Fatal(err)
	}
	client := ssh.NewClient(cc, chans, reqs)
	defer client.Close()

	// 4) sample LOCAL FORWARD: -L 127.0.0.1:10022 -> receiver:127.0.0.1:22
	go localForward(client, "127.0.0.1:10022", "127.0.0.1:22")
	fmt.Println("try: ssh -p 10022 localhost")

	// 5) open interactive shell
	if err := interactiveShell(client); err != nil {
		log.Fatal(err)
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
			rc, err := c.Dial("tcp", target) // opens direct-tcpip channel
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
	return sess.Shell() // blocks until exit; Ctrl+D to close
}

// Run executes the sender command
func Run(code string) error {
	if code == "" {
		return fmt.Errorf("code is required")
	}
	startSSHClient(code)
	return nil
}
