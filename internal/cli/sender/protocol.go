package sender

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// --- Tuning / limits ---
const (
	maxHeaderBytes = 32 * 1024
	maxHeaderLines = 200
	clockSkew      = 30 * time.Second
)

// Set SSH_PORTAL_DEBUG env var to enable debug
var debugProtocol = os.Getenv("SSH_PORTAL_DEBUG") != ""

// --- Protocol structures ---

type Greeting struct {
	Proto   string            // must be ssh-relay/1
	OK      bool              // true if "OK", false if "ERR ..."
	ErrText string            // optional message after ERR
	KV      map[string]string // key=value lines (optional)
}

type ConnectionResult struct {
	Conn         net.Conn // raw socket
	SSHConn      net.Conn // reader positioned at SSH banner
	Fingerprint  string   // from kv["fp"]
	ClientConfig *ssh.ClientConfig
}

// --- Entry point ---

func ConnectAndHandshake(relayAddr, code string) (*ConnectionResult, error) {
	// 1) Connect
	sock, err := net.Dial("tcp", relayAddr)
	if err != nil {
		return nil, fmt.Errorf("connect relay: %w", err)
	}

	// 2) Send HELLO
	if _, err := fmt.Fprintf(sock, "HELLO sender code=%s\n", code); err != nil {
		sock.Close()
		return nil, fmt.Errorf("send HELLO: %w", err)
	}

	// 3) Read strict greeting block (leaves SSH banner buffered)
	br := bufio.NewReader(sock)
	gr, raw, err := readStrictGreeting(br)
	if debugProtocol && raw != "" {
		fmt.Fprintf(os.Stderr, "\n=== Relay Greeting ===\n%s=== END ===\n\n", raw)
	}
	if err != nil {
		sock.Close()
		return nil, err
	}

	// 4) Validate greeting
	if gr.Proto != "ssh-relay/1" {
		sock.Close()
		return nil, fmt.Errorf("unsupported protocol %q", gr.Proto)
	}
	if !gr.OK {
		msg := gr.ErrText
		if msg == "" {
			msg = "relay reported error"
		}
		sock.Close()
		return nil, fmt.Errorf("relay error: %s", msg)
	}

	fp := strings.TrimSpace(gr.KV["fp"])
	if fp == "" {
		sock.Close()
		return nil, fmt.Errorf("missing required key: fp")
	}

	// Optional: exp + alg
	if expStr := strings.TrimSpace(gr.KV["exp"]); expStr != "" {
		sec, err := strconv.ParseInt(expStr, 10, 64)
		if err != nil {
			sock.Close()
			return nil, fmt.Errorf("bad exp: %w", err)
		}
		exp := time.Unix(sec, 0)
		if time.Now().After(exp.Add(clockSkew)) {
			sock.Close()
			return nil, fmt.Errorf("relay token expired at %s", exp.UTC().Format(time.RFC3339))
		}
	}

	// 5) Build a connection that starts reading exactly at the SSH banner
	sshConn := &prebufConn{Conn: sock, r: io.MultiReader(br, sock)}

	// 6) Create pinned SSH client config
	cfg := &ssh.ClientConfig{
		User: code,
		Auth: []ssh.AuthMethod{ssh.Password(code)},
		HostKeyCallback: func(host string, addr net.Addr, key ssh.PublicKey) error {
			got := ssh.FingerprintSHA256(key)
			if got != fp {
				return fmt.Errorf("host key mismatch: got %s, want %s", got, fp)
			}
			return nil
		},
	}

	// Optional: print nice info
	if alg := strings.TrimSpace(gr.KV["alg"]); alg != "" && gr.KV["exp"] != "" {
		fmt.Printf("Pinned receiver fp: %s (exp %s, alg %s)\n", fp, gr.KV["exp"], alg)
	} else if alg != "" {
		fmt.Printf("Pinned receiver fp: %s (alg %s)\n", fp, alg)
	} else if gr.KV["exp"] != "" {
		fmt.Printf("Pinned receiver fp: %s (exp %s)\n", fp, gr.KV["exp"])
	} else {
		fmt.Println("Pinned receiver fp:", fp)
	}

	return &ConnectionResult{
		Conn:         sock,
		SSHConn:      sshConn,
		Fingerprint:  fp,
		ClientConfig: cfg,
	}, nil
}

// --- Strict greeting parser ---

// readStrictGreeting parses exactly:
//
//	proto: ssh-relay/1
//	OK | ERR[ <text>]
//	key=value      (0+ lines; must be '=' form)
//	<blank line>   (terminator; exactly one)
//
// After the blank line, the next byte must be 'S' of "SSH-"; we do not consume it.
// We return the raw header text for optional debugging.
func readStrictGreeting(br *bufio.Reader) (Greeting, string, error) {
	var raw strings.Builder
	write := func(s string) { raw.WriteString(s); raw.WriteByte('\n') }

	var g Greeting
	g.KV = make(map[string]string)

	totalBytes := 0
	lineCount := 0

	readLine := func() (string, error) {
		s, err := br.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("read line: %w", err)
		}
		lineCount++
		totalBytes += len(s)
		if totalBytes > maxHeaderBytes || lineCount > maxHeaderLines {
			return "", fmt.Errorf("greeting too large")
		}

		// keep raw as-is (including trailing \n)
		write(strings.TrimRight(s, "\n"))
		return strings.TrimRight(s, "\r\n"), nil
	}

	// 1) proto: ssh-relay/1
	l, err := readLine()
	if err != nil {
		return g, raw.String(), err
	}
	if !strings.HasPrefix(strings.ToLower(l), "proto:") {
		return g, raw.String(), fmt.Errorf("expected 'proto:' line, got %q", l)
	}
	g.Proto = strings.TrimSpace(strings.TrimPrefix(l, l[:strings.Index(l, ":")+1]))
	if g.Proto == "" {
		return g, raw.String(), fmt.Errorf("empty proto")
	}

	// 2) OK | ERR[ text]
	l, err = readLine()
	if err != nil {
		return g, raw.String(), err
	}
	switch {
	case l == "OK":
		g.OK = true
	case strings.HasPrefix(l, "ERR"):
		g.OK = false
		g.ErrText = strings.TrimSpace(strings.TrimPrefix(l, "ERR"))
	default:
		return g, raw.String(), fmt.Errorf("expected OK or ERR, got %q", l)
	}

	if !g.OK {
		// If error, do not parse key=values, just return now.
		return g, raw.String(), fmt.Errorf("relay error: %s", g.ErrText)
	}

	// 3) zero or more key=value lines until a single blank line
	for {
		peek, _ := br.Peek(1)
		if len(peek) == 0 {
			return g, raw.String(), fmt.Errorf("unexpected EOF before banner")
		}
		// We need to read the line to both validate and advance to the terminator.
		l, err = readLine()
		if err != nil {
			return g, raw.String(), err
		}

		if l == "" { // the single, terminating blank line
			break
		}

		k, v, ok := strings.Cut(l, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return g, raw.String(), fmt.Errorf("invalid header %q (expected key=value)", l)
		}
		// Do not trim spaces inside value; keep exactly what server sent after '='
		g.KV[strings.TrimSpace(k)] = v
	}

	// 4) Next byte must start the SSH banner, but we don't consume it
	// (We can cheaply check without advancing.)
	b, err := br.Peek(4)
	if err != nil {
		return g, raw.String(), fmt.Errorf("peek banner: %w", err)
	}
	if string(b) != "SSH-" {
		return g, raw.String(), fmt.Errorf("expected SSH banner after blank line")
	}

	return g, raw.String(), nil
}

// --- Small adapter to expose buffered bytes first, then the socket ---

type prebufConn struct {
	net.Conn
	r io.Reader
}

func (c *prebufConn) Read(p []byte) (int, error) { return c.r.Read(p) }
