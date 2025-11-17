package sender

import (
	"fmt"
	"os"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// interactiveShell launches an interactive SSH shell session
func interactiveShell(c *ssh.Client) error {
	sess, err := c.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()

	// Get terminal size
	width, height := 80, 24 // Defaults
	if fd := int(os.Stdin.Fd()); term.IsTerminal(fd) {
		if w, h, err := term.GetSize(fd); err == nil {
			width, height = w, h
		}
	}

	// Request PTY with actual terminal size
	if err := sess.RequestPty("xterm-256color", height, width, ssh.TerminalModes{}); err != nil {
		return fmt.Errorf("failed to request PTY: %w", err)
	}

	sess.Stdin = os.Stdin
	sess.Stdout = os.Stdout
	sess.Stderr = os.Stderr
	return sess.Shell()
}

