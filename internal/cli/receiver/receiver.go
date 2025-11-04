package receiver

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
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

	"errors"
	"ssh-portal/internal/cli/usercode"
	"ssh-portal/internal/version"
)

var (
	// errConnectionClosed is returned when the SSH connection closes normally
	// This signals that we should restart and reconnect
	errConnectionClosed = errors.New("connection closed")
)

// DirectTCPIP represents an active direct-tcpip forwarding connection
type DirectTCPIP struct {
	ID         string
	SrcAddress string // Sender address from ready message
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

// ReverseTCPIP represents an active reverse (tcpip-forward) connection
type ReverseTCPIP struct {
	ID         string
	ListenAddr string
	ListenPort uint32
	OriginAddr string
	OriginPort uint32
	CreatedAt  time.Time
	Listener   net.Listener
}

var (
	reverseTCPIPMu sync.RWMutex
	reverseTCPIPs  = make(map[string]*ReverseTCPIP)
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

// GetAllReverseTCPIPs returns all active reverse-tcpip connections
func GetAllReverseTCPIPs() []*ReverseTCPIP {
	reverseTCPIPMu.RLock()
	defer reverseTCPIPMu.RUnlock()

	result := make([]*ReverseTCPIP, 0, len(reverseTCPIPs))
	for _, r := range reverseTCPIPs {
		result = append(result, r)
	}
	return result
}

// cleanupConnections closes all active direct-tcpip and reverse-tcpip connections
func cleanupConnections() {
	// Close all direct-tcpip connections
	directTCPIPMu.Lock()
	for _, dtcp := range directTCPIPs {
		if dtcp.Channel != nil {
			dtcp.Channel.Close()
		}
	}
	directTCPIPs = make(map[string]*DirectTCPIP)
	directTCPIPMu.Unlock()

	// Close all reverse-tcpip listeners
	reverseTCPIPMu.Lock()
	for _, r := range reverseTCPIPs {
		if r.Listener != nil {
			r.Listener.Close()
		}
	}
	reverseTCPIPs = make(map[string]*ReverseTCPIP)
	reverseTCPIPMu.Unlock()
}

func startSSHServer(relayHost string, relayPort int, enableSession bool, interactive bool) error {
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
	relayConn := connResult.Conn
	// Note: relayConn will be owned by sshConn after SSH handshake, so we don't defer close here
	// We'll close it explicitly if we return before SSH is established

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
	if !interactive {
		fmt.Println("Code      :", userCode)
		fmt.Println("RelayCode :", mintResp.Code)
		fmt.Println("RID       :", mintResp.RID)
		fmt.Println("FP        :", fp)
		fmt.Println("Waiting for sender to connect...")
	}

	// 4) Wait for "ready" message (sender has connected)
	ready, err := WaitForReady(relayConn)
	if err != nil {
		SetError(fmt.Sprintf("failed to receive ready message: %v", err))
		log.Printf("failed to receive ready message: %v", err)
		ClearState()
		relayConn.Close()
		return err
	}
	// Build log message with identity if available
	logMsg := fmt.Sprintf("Received ready from relay: sender=%s fp=%s", ready.SenderAddr, ready.Fingerprint)
	if ready.Sender != nil && ready.Sender.Identity != "" {
		// Decode base64 identity
		decodedIdentity, err := base64.StdEncoding.DecodeString(ready.Sender.Identity)
		if err != nil {
			log.Printf("Failed to decode sender identity: %v", err)
			// Use encoded value as fallback
			SetSenderIdentity(ready.Sender.Identity)
			logMsg += " identity=<decode-error>"
		} else {
			identity := string(decodedIdentity)
			SetSenderIdentity(identity)
			logMsg += fmt.Sprintf(" identity=%s", identity)
		}
	}
	log.Printf("%s", logMsg)
	SetSenderAddr(ready.SenderAddr)

	// 5) Setup SSH server over the connection (now ready for SSH handshake)
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			expectedUsername := mintResp.Code
			expectedPassword := fullCode
			if c.User() != expectedUsername || string(pass) != expectedPassword {
				// Log when sender connects but fails authentication
				senderAddr := ready.SenderAddr
				if senderAddr == "" {
					senderAddr = c.RemoteAddr().String()
				}
				log.Printf("Sender connected but failed password authentication: sender=%s username=%s", senderAddr, c.User())
				return nil, fmt.Errorf("invalid credentials")
			}
			return nil, nil
		},
	}
	cfg.AddHostKey(signer)

	sshConn, chans, reqs, err := ssh.NewServerConn(relayConn, cfg)
	if err != nil {
		SetError(fmt.Sprintf("SSH server connection failed: %v", err))
		log.Printf("SSH server connection failed: %v", err)
		relayConn.Close()
		return err
	}
	// sshConn now owns relayConn, so closing sshConn will close relayConn
	defer sshConn.Close()

	state := GetState()
	senderAddr := state.SenderAddr
	if senderAddr == "" {
		senderAddr = sshConn.RemoteAddr().String()
	}
	log.Printf("SSH connection established with sender: %s", senderAddr)
	SetSSHEstablished()

	// Handle keepalive requests and monitor connection health
	keepaliveTimeout := 30 * time.Second
	if ready.Sender != nil && ready.Sender.Keepalive > 0 {
		keepaliveTimeout = time.Duration(ready.Sender.Keepalive) * time.Second
	}
	lastKeepalive := time.Now()
	keepaliveMu := &sync.Mutex{}

	// Monitor for missed keepalives
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			keepaliveMu.Lock()
			last := lastKeepalive
			keepaliveMu.Unlock()

			if time.Since(last) > keepaliveTimeout {
				log.Printf("Keepalive timeout, sender connection appears dead, closing SSH connection")
				SetError("Sender connection timeout")
				// Close the connection to trigger channel loop exit
				sshConn.Close()
				return
			}
		}
	}()

	// Handle global requests (remote-forward control and keepalive)
	go handleGlobal(reqs, sshConn, keepaliveMu, &lastKeepalive)

	// Handle channels - when this loop exits, the connection is closed
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

	// Channel loop exited - connection closed
	log.Printf("SSH connection closed, cleaning up")

	// Clean up all connections and state
	cleanupConnections()
	ClearState()

	// sshConn.Close() is already deferred, which will close the underlying relayConn
	return errConnectionClosed
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

	// Get sender address from state
	state := GetState()
	srcAddr := state.SenderAddr
	if srcAddr == "" {
		srcAddr = "unknown"
	}

	// Create and track the direct-tcpip connection
	dtcp := &DirectTCPIP{
		ID:         fmt.Sprintf("%d", time.Now().UnixNano()),
		SrcAddress: srcAddr,
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

func handleGlobal(reqs <-chan *ssh.Request, conn *ssh.ServerConn, keepaliveMu *sync.Mutex, lastKeepalive *time.Time) {
	for req := range reqs {
		switch req.Type {
		case "keepalive@ssh-portal":
			// Handle keepalive request
			keepaliveMu.Lock()
			*lastKeepalive = time.Now()
			keepaliveMu.Unlock()
			req.Reply(true, nil)
			continue
		case "tcpip-forward":
			// Payload: string address_to_bind, uint32 port
			var msg struct {
				Address string
				Port    uint32
			}
			if err := ssh.Unmarshal(req.Payload, &msg); err != nil {
				log.Printf("[R-FWD] bad tcpip-forward payload: %v", err)
				req.Reply(false, nil)
				continue
			}
			bindAddr := msg.Address
			bindPort := msg.Port
			host := net.JoinHostPort(bindAddr, strconv.FormatUint(uint64(bindPort), 10))
			if bindPort == 0 {
				host = net.JoinHostPort(bindAddr, "0")
			}
			ln, err := net.Listen("tcp", host)
			if err != nil {
				log.Printf("[R-FWD] listen failed on %s: %v", host, err)
				req.Reply(false, nil)
				continue
			}
			// If port was zero, reply with the allocated port
			actualPort := uint32(ln.Addr().(*net.TCPAddr).Port)
			if bindPort == 0 {
				// reply expects a uint32 port
				reply := struct{ Port uint32 }{Port: actualPort}
				req.Reply(true, ssh.Marshal(reply))
			} else {
				req.Reply(true, nil)
			}

			id := fmt.Sprintf("%d", time.Now().UnixNano())
			rf := &ReverseTCPIP{
				ID:         id,
				ListenAddr: bindAddr,
				ListenPort: actualPort,
				CreatedAt:  time.Now(),
				Listener:   ln,
			}
			reverseTCPIPMu.Lock()
			reverseTCPIPs[id] = rf
			reverseTCPIPMu.Unlock()
			log.Printf("[R-FWD] listening id=%s on %s:%d", id, bindAddr, actualPort)

			// Accept loop
			go func(id string, ln net.Listener, bindAddr string, bindPort uint32) {
				for {
					lc, err := ln.Accept()
					if err != nil {
						// Listener closed
						log.Printf("[R-FWD] listener closed id=%s: %v", id, err)
						return
					}
					// On accept, open a forwarded-tcpip channel to the sender
					go func(lc net.Conn) {
						defer lc.Close()
						orgHost, orgPortStr, _ := net.SplitHostPort(lc.RemoteAddr().String())
						var orgPort64 uint64
						if orgPortStr != "" {
							if p, err := strconv.ParseUint(orgPortStr, 10, 32); err == nil {
								orgPort64 = p
							}
						}
						// Update entry for display
						reverseTCPIPMu.Lock()
						if r := reverseTCPIPs[id]; r != nil {
							r.OriginAddr = orgHost
							r.OriginPort = uint32(orgPort64)
						}
						reverseTCPIPMu.Unlock()

						// Prepare forwarded-tcpip payload
						type fwdPayload struct {
							BindAddr   string
							BindPort   uint32
							OriginAddr string
							OriginPort uint32
						}
						payload := ssh.Marshal(fwdPayload{bindAddr, bindPort, orgHost, uint32(orgPort64)})
						ch, reqs, err := conn.OpenChannel("forwarded-tcpip", payload)
						if err != nil {
							log.Printf("[R-FWD] open forwarded-tcpip failed: %v", err)
							return
						}
						go discard(reqs)
						// Bridge data
						go io.Copy(ch, lc)
						_, _ = io.Copy(lc, ch)
						ch.Close()
					}(lc)
				}
			}(id, ln, bindAddr, actualPort)

		case "cancel-tcpip-forward":
			// Payload: string address_to_bind, uint32 port
			var msg struct {
				Address string
				Port    uint32
			}
			if err := ssh.Unmarshal(req.Payload, &msg); err != nil {
				log.Printf("[R-FWD] bad cancel payload: %v", err)
				req.Reply(false, nil)
				continue
			}
			// Find matching listener
			var closed bool
			reverseTCPIPMu.Lock()
			for id, r := range reverseTCPIPs {
				if r.ListenAddr == msg.Address && r.ListenPort == msg.Port {
					r.Listener.Close()
					delete(reverseTCPIPs, id)
					closed = true
					break
				}
			}
			reverseTCPIPMu.Unlock()
			req.Reply(closed, nil)
		default:
			req.Reply(false, nil)
		}
	}
}

// Run executes the receiver command
func Run(relayHost string, relayPort int, interactive bool, session bool) error {
	log.Printf("Starting receiver version %s", version.String())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if interactive {
		// Start TUI for interactive mode
		if err := startTUI(ctx, cancel); err != nil {
			return fmt.Errorf("failed to start TUI: %w", err)
		}
	}

	// Start SSH server in a goroutine with restart loop
	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Printf("Context cancelled, stopping receiver")
				return
			default:
				err := startSSHServer(relayHost, relayPort, session, interactive)
				if err == nil {
					// Should not happen, but if it does, exit
					log.Printf("SSH server returned without error, exiting")
					return
				}

				if err == errConnectionClosed {
					// Connection closed - restart after a brief delay
					log.Printf("Sender disconnected, restarting receiver in 1 second...")
					time.Sleep(1 * time.Second)
					continue
				}

				// Other errors - log and retry after delay
				log.Printf("SSH server error: %v, retrying in 2 seconds...", err)
				time.Sleep(2 * time.Second)
			}
		}
	}()

	// Wait for shutdown signal
	<-ctx.Done()
	log.Printf("receiver shutting down...")
	return nil
}
