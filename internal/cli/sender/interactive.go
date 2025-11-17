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

	// Get terminal file descriptor
	fd := int(os.Stdin.Fd())

	// Get terminal size
	width, height := 80, 24 // Defaults
	if term.IsTerminal(fd) {
		if w, h, err := term.GetSize(fd); err == nil {
			width, height = w, h
		}
	}

	// Request PTY with actual terminal size
	if err := sess.RequestPty("xterm-256color", height, width, ssh.TerminalModes{}); err != nil {
		return fmt.Errorf("failed to request PTY: %w", err)
	}

	// Put terminal into raw mode if it's a terminal
	var oldState *term.State
	if term.IsTerminal(fd) {
		oldState, err = term.MakeRaw(fd)
		if err != nil {
			return fmt.Errorf("failed to set raw terminal: %w", err)
		}
		defer term.Restore(fd, oldState)
	}

	sess.Stdin = os.Stdin
	sess.Stdout = os.Stdout
	sess.Stderr = os.Stderr

	// Start the shell
	if err := sess.Shell(); err != nil {
		return fmt.Errorf("failed to start shell: %w", err)
	}

	// Wait for the shell to exit
	return sess.Wait()
}
