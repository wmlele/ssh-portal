package receiver

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"github.com/creack/pty"
	"golang.org/x/crypto/ssh"
)

const (
	relayHTTP = "http://127.0.0.1:8080/mint" // adjust
	relayTCP  = "127.0.0.1:4430"             // adjust
)

type mintResp struct {
	Code string `json:"code"`
	RID  string `json:"rid"`
	Exp  string `json:"exp"`
}

func startSSHServer() {
	// 1) host key (ephemeral; persist if you want TOFU)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromSigner(priv)
	fp := ssh.FingerprintSHA256(signer.PublicKey())

	// 2) mint code
	body, _ := json.Marshal(map[string]any{"receiver_fp": fp})
	resp, err := http.Post(relayHTTP, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Fatal(err)
	}
	var m mintResp
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		log.Fatal(err)
	}
	_ = resp.Body.Close()
	fmt.Println("Code:", m.Code)
	fmt.Println("RID :", m.RID)
	fmt.Println("FP  :", fp)

	// 3) outbound to relay + HELLO
	conn, err := net.Dial("tcp", relayTCP)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(conn, "HELLO receiver rid=%s\n", m.RID)

	// 4) SSH server over that socket
	cfg := &ssh.ServerConfig{
		NoClientAuth: true, // relay already authorized the pairing; you can add more auth here if desired
	}
	cfg.AddHostKey(signer)

	sshConn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer sshConn.Close()
	go handleGlobal(reqs, sshConn) // remote-forward control (no-op for now)
	for ch := range chans {
		switch ch.ChannelType() {
		case "session":
			channel, reqs, _ := ch.Accept()
			go handleSession(channel, reqs)
		case "direct-tcpip":
			// client requests we dial a target and bridge
			payload := ch.ExtraData()
			var msg struct {
				DestAddr   string
				DestPort   uint32
				OriginAddr string
				OriginPort uint32
			}
			if err := ssh.Unmarshal(payload, &msg); err != nil {
				ch.Reject(ssh.ConnectionFailed, "bad payload")
				continue
			}
			channel, reqs, _ := ch.Accept()
			go discard(reqs)
			dst := fmt.Sprintf("%s:%d", msg.DestAddr, msg.DestPort)
			up, err := net.Dial("tcp", dst)
			if err != nil {
				channel.Close()
				continue
			}
			go io.Copy(up, channel)
			go func() { io.Copy(channel, up); channel.Close(); up.Close() }()
		default:
			ch.Reject(ssh.UnknownChannelType, "unsupported")
		}
	}
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
func Run() error {
	fmt.Println("receiver")
	startSSHServer()
	return nil
}
