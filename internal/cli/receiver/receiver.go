package receiver

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/creack/pty"
	"golang.org/x/crypto/ssh"

	"ssh-portal/internal/cli/usercode"
)

// DirectTCPIP represents an active direct-tcpip forwarding connection
type DirectTCPIP struct {
	ID         string
	DestAddr   string
	DestPort   uint32
	OriginAddr string
	OriginPort uint32
	CreatedAt  time.Time
	Channel    ssh.Channel
}

var (
	directTCPIPMu sync.RWMutex
	directTCPIPs  = make(map[string]*DirectTCPIP)
)

// GetAllDirectTCPIPs returns all active direct-tcpip forwarding connections
func GetAllDirectTCPIPs() []*DirectTCPIP {
	directTCPIPMu.RLock()
	defer directTCPIPMu.RUnlock()

	result := make([]*DirectTCPIP, 0, len(directTCPIPs))
	for _, dtcp := range directTCPIPs {
		result = append(result, dtcp)
	}
	return result
}

func startSSHServer(relayHost string, relayPort int, enableSession bool) error {
	// 1) Generate host key (ephemeral; persist if you want TOFU)
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		SetError(fmt.Sprintf("failed to generate host key: %v", err))
		log.Printf("failed to generate host key: %v", err)
		return err
	}
	signer, err := ssh.NewSignerFromSigner(priv)
	if err != nil {
		SetError(fmt.Sprintf("failed to create signer: %v", err))
		log.Printf("failed to create signer: %v", err)
		return err
	}
	fp := ssh.FingerprintSHA256(signer.PublicKey())

	// 2) Connect to relay and perform protocol handshake (mint + hello)
	connResult, mintResp, err := ConnectToRelay(relayHost, relayPort, fp)
	if err != nil {
		SetError(fmt.Sprintf("failed to connect to relay: %v", err))
		log.Printf("failed to connect to relay: %v", err)
		return err
	}
	defer connResult.Conn.Close()

	// 3) Generate receiver code and user code, then store state for TUI
	localSecret, err := usercode.GenerateReceiverCode()
	if err != nil {
		SetError(fmt.Sprintf("failed to generate receiver code: %v", err))
		log.Printf("failed to generate receiver code: %v", err)
		return err
	}

	userCode, fullCode, err := usercode.GenerateUserCode(mintResp.Code, localSecret)
	if err != nil {
		SetError(fmt.Sprintf("failed to generate user code: %v", err))
		log.Printf("failed to generate user code: %v", err)
		return err
	}

	SetState(userCode, mintResp.Code, localSecret, mintResp.RID, fp)
	fmt.Println("Code      :", userCode)
	fmt.Println("RelayCode :", mintResp.Code)
	fmt.Println("RID       :", mintResp.RID)
	fmt.Println("FP        :", fp)
	fmt.Println("Waiting for sender to connect...")

	// 4) Wait for "ready" message (sender has connected)
	ready, err := WaitForReady(connResult.Conn)
	if err != nil {
		SetError(fmt.Sprintf("failed to receive ready message: %v", err))
		log.Printf("failed to receive ready message: %v", err)
		return err
	}
	log.Printf("Received ready from relay: sender=%s fp=%s", ready.SenderAddr, ready.Fingerprint)
	SetSenderAddr(ready.SenderAddr)

	// 5) Setup SSH server over the connection (now ready for SSH handshake)
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			expectedUsername := mintResp.Code
			expectedPassword := fullCode
			if c.User() != expectedUsername || string(pass) != expectedPassword {
				return nil, fmt.Errorf("invalid credentials")
			}
			return nil, nil
		},
	}
	cfg.AddHostKey(signer)

	sshConn, chans, reqs, err := ssh.NewServerConn(connResult.Conn, cfg)
	if err != nil {
		SetError(fmt.Sprintf("SSH server connection failed: %v", err))
		log.Printf("SSH server connection failed: %v", err)
		return err
	}
	defer sshConn.Close()

	state := GetState()
	senderAddr := state.SenderAddr
	if senderAddr == "" {
		senderAddr = sshConn.RemoteAddr().String()
	}
	log.Printf("SSH connection established with sender: %s", senderAddr)
	SetSSHEstablished()

	// Handle global requests (remote-forward control)
	go handleGlobal(reqs, sshConn)

	// Handle channels
	for ch := range chans {
		switch ch.ChannelType() {
		case "session":
			if !enableSession {
				log.Printf("SSH session channel rejected (session handling disabled)")
				ch.Reject(ssh.Prohibited, "session handling disabled")
				continue
			}
			channel, reqs, _ := ch.Accept()
			log.Printf("SSH session channel opened by sender")
			go handleSession(channel, reqs)
		case "direct-tcpip":
			handleDirectTCPIP(ch)
		default:
			ch.Reject(ssh.UnknownChannelType, "unsupported")
		}
	}
	// This should never be reached as the loop runs indefinitely
	// but required for the function signature
	return nil
}

// handleDirectTCPIP handles direct-tcpip channel requests (port forwarding)
func handleDirectTCPIP(ch ssh.NewChannel) {
	payload := ch.ExtraData()
	var msg struct {
		DestAddr   string
		DestPort   uint32
		OriginAddr string
		OriginPort uint32
	}
	if err := ssh.Unmarshal(payload, &msg); err != nil {
		ch.Reject(ssh.ConnectionFailed, "bad payload")
		return
	}
	channel, reqs, _ := ch.Accept()
	go discard(reqs)

	// Create and track the direct-tcpip connection
	dtcp := &DirectTCPIP{
		ID:         fmt.Sprintf("%d", time.Now().UnixNano()),
		DestAddr:   msg.DestAddr,
		DestPort:   msg.DestPort,
		OriginAddr: msg.OriginAddr,
		OriginPort: msg.OriginPort,
		CreatedAt:  time.Now(),
		Channel:    channel,
	}

	// Register the connection
	directTCPIPMu.Lock()
	directTCPIPs[dtcp.ID] = dtcp
	directTCPIPMu.Unlock()

	// Log the connection creation
	log.Printf("[DIRECT-TCPIP] created: id=%s origin=%s:%d -> dest=%s:%d",
		dtcp.ID, msg.OriginAddr, msg.OriginPort, msg.DestAddr, msg.DestPort)

	dst := net.JoinHostPort(msg.DestAddr, strconv.FormatUint(uint64(msg.DestPort), 10))
	up, err := net.Dial("tcp", dst)
	if err != nil {
		log.Printf("[DIRECT-TCPIP] connection failed: id=%s dest=%s error=%v", dtcp.ID, dst, err)
		channel.Close()
		// Remove from registry on failure
		directTCPIPMu.Lock()
		delete(directTCPIPs, dtcp.ID)
		directTCPIPMu.Unlock()
		return
	}

	// Start forwarding in both directions
	go io.Copy(up, channel)
	go func() {
		io.Copy(channel, up)
		channel.Close()
		up.Close()

		// Remove from registry when connection closes
		directTCPIPMu.Lock()
		delete(directTCPIPs, dtcp.ID)
		directTCPIPMu.Unlock()

		log.Printf("[DIRECT-TCPIP] closed: id=%s origin=%s:%d -> dest=%s:%d",
			dtcp.ID, msg.OriginAddr, msg.OriginPort, msg.DestAddr, msg.DestPort)
	}()
}

func discard(reqs <-chan *ssh.Request) {
	for range reqs {
	}
}

// session: simple PTY shell
func handleSession(ch ssh.Channel, in <-chan *ssh.Request) {
	var ptyFile *os.File
	var shell *exec.Cmd
	for req := range in {
		switch req.Type {
		case "pty-req":
			term, w, h := parsePtyReq(req.Payload)
			shell = exec.Command(userShell())
			shell.Env = append(os.Environ(), "TERM="+term)
			f, err := pty.Start(shell)
			if err != nil {
				req.Reply(false, nil)
				ch.Close()
				return
			}
			ptyFile = f
			setWinsize(ptyFile, h, w)
			go io.Copy(ptyFile, ch)
			go func() { io.Copy(ch, ptyFile); ch.Close() }()
			req.Reply(true, nil)
		case "shell":
			if ptyFile == nil {
				// no PTY requested: run non-pty shell
				shell = exec.Command(userShell(), "-l")
				shell.Stdin = ch
				shell.Stdout = ch
				shell.Stderr = ch.Stderr()
				_ = shell.Start()
				go func() { shell.Wait(); ch.Close() }()
			}
			req.Reply(true, nil)
		case "exec":
			// run single command without PTY
			var payload struct {
				Len uint32
				Cmd []byte
			}
			ssh.Unmarshal(req.Payload, &payload)
			cmd := exec.Command("/bin/sh", "-c", string(payload.Cmd))
			cmd.Stdin = ch
			cmd.Stdout = ch
			cmd.Stderr = ch.Stderr()
			_ = cmd.Start()
			go func() { cmd.Wait(); ch.Close() }()
			req.Reply(true, nil)
		case "window-change":
			if ptyFile != nil {
				_, w, h := parseWinChg(req.Payload)
				setWinsize(ptyFile, h, w)
			}
		default:
			req.Reply(false, nil)
		}
	}
}

func userShell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "/bin/bash"
}

// unmarshalString parses an SSH string: 4-byte length (big-endian) + string data
func unmarshalString(b []byte) (string, []byte, error) {
	if len(b) < 4 {
		return "", nil, fmt.Errorf("insufficient data for string length")
	}
	length := binary.BigEndian.Uint32(b[:4])
	if len(b) < 4+int(length) {
		return "", nil, fmt.Errorf("insufficient data for string: want %d, have %d", 4+length, len(b))
	}
	str := string(b[4 : 4+length])
	return str, b[4+length:], nil
}

// unmarshalUint32 parses a big-endian uint32 from the first 4 bytes
func unmarshalUint32(b []byte) uint32 {
	if len(b) < 4 {
		return 0
	}
	return binary.BigEndian.Uint32(b[:4])
}

func parsePtyReq(b []byte) (term string, cols, rows uint32) {
	// string term; uint32 cols, rows, px, py; string modes
	term, rest, _ := unmarshalString(b)
	cols = unmarshalUint32(rest)
	rows = unmarshalUint32(rest[4:])
	return term, cols, rows
}
func parseWinChg(b []byte) (w, c, r uint32) {
	c = unmarshalUint32(b)     // cols
	r = unmarshalUint32(b[4:]) // rows
	return 0, c, r
}
func setWinsize(f *os.File, h, w uint32) {
	ws := &struct{ Row, Col, X, Y uint16 }{uint16(h), uint16(w), 0, 0}
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(syscall.TIOCSWINSZ), uintptr(unsafe.Pointer(ws)))
}

func handleGlobal(reqs <-chan *ssh.Request, _ *ssh.ServerConn) {
	// TODO: implement tcpip-forward / cancel-tcpip-forward to support -R
	for req := range reqs {
		switch req.Type {
		default:
			req.Reply(false, nil)
		}
	}
}

// Run executes the receiver command
func Run(relayHost string, relayPort int, interactive bool, session bool) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if interactive {
		// Start TUI for interactive mode
		if err := startTUI(ctx, cancel); err != nil {
			return fmt.Errorf("failed to start TUI: %w", err)
		}
	}

	// Start SSH server in a goroutine (it runs indefinitely or until error)
	go func() {
		if err := startSSHServer(relayHost, relayPort, session); err != nil {
			// Error already logged and set in state view
			log.Printf("SSH server failed to start, but keeping receiver running for manual quit")
		}
	}()

	// Wait for shutdown signal
	<-ctx.Done()
	log.Printf("receiver shutting down...")
	return nil
}
