package sender

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// shellExitMsg is sent back to the TUI after a shell session ends
type shellExitMsg struct {
	err error
}

// sshShellCmd implements bubbletea's tea.ExecCommand interface
// to run an interactive SSH shell session
type sshShellCmd struct {
	client *ssh.Client
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

// NewShellCmd creates a new sshShellCmd for the given SSH client
func NewShellCmd(client *ssh.Client) *sshShellCmd {
	return &sshShellCmd{client: client}
}

func (c *sshShellCmd) SetStdin(r io.Reader)  { c.stdin = r }
func (c *sshShellCmd) SetStdout(w io.Writer) { c.stdout = w }
func (c *sshShellCmd) SetStderr(w io.Writer) { c.stderr = w }

// Run opens an SSH session, requests a PTY, starts a shell, and blocks until it exits
func (c *sshShellCmd) Run() error {
	// Determine terminal type
	termType := os.Getenv("TERM")
	if termType == "" {
		termType = "xterm-256color"
	}

	// Get stdin file descriptor for terminal operations
	stdin, ok := c.stdin.(*os.File)
	if !ok {
		stdin = os.Stdin
	}
	fd := int(stdin.Fd())

	// Get terminal size
	width, height, err := term.GetSize(fd)
	if err != nil {
		width = 80
		height = 24
	}

	// Put terminal in raw mode
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("failed to set raw terminal: %w", err)
	}
	defer term.Restore(fd, oldState)

	// Open SSH session
	sess, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to open session: %w", err)
	}
	defer sess.Close()

	// Request PTY
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := sess.RequestPty(termType, height, width, modes); err != nil {
		return fmt.Errorf("failed to request PTY: %w", err)
	}

	// Handle SIGWINCH to propagate window size changes
	sigwinch := make(chan os.Signal, 1)
	signal.Notify(sigwinch, syscall.SIGWINCH)
	defer signal.Stop(sigwinch)

	go func() {
		for range sigwinch {
			w, h, err := term.GetSize(fd)
			if err == nil {
				sess.WindowChange(h, w)
			}
		}
	}()

	// Wire up I/O
	sess.Stdin = c.stdin
	sess.Stdout = c.stdout
	sess.Stderr = c.stderr

	// Start shell and wait
	if err := sess.Shell(); err != nil {
		return fmt.Errorf("failed to start shell: %w", err)
	}

	return sess.Wait()
}

// GetSSHClient returns the current SSH client, or nil if not connected
func GetSSHClient() *ssh.Client {
	sshClientMu.RLock()
	defer sshClientMu.RUnlock()
	return sshClient
}
