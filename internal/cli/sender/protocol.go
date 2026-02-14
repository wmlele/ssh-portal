package sender

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"ssh-portal/internal/cli/usercode"

	//"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// --- Tuning / limits ---
const (
	clockSkew = 30 * time.Second
)

// Set SSH_PORTAL_DEBUG env var to enable debug
var debugProtocol = os.Getenv("SSH_PORTAL_DEBUG") != ""

// --- Protocol structures ---

// JSONHello is the JSON hello message sent to the relay before SSH starts
type JSONHello struct {
	Msg    string      `json:"msg"`
	Role   string      `json:"role"`
	Code   string      `json:"code,omitempty"`
	RID    string      `json:"rid,omitempty"`
	Sender *SenderInfo `json:"sender,omitempty"`
	Token  string      `json:"token,omitempty"`
}

// JSONOKResponse is the JSON success response sent back by the relay
type JSONOKResponse struct {
	Msg string `json:"msg"`
	FP  string `json:"fp"`
	Exp int64  `json:"exp"`
	Alg string `json:"alg"`
}

// JSONErrorResponse is the JSON error response sent back by the relay
type JSONErrorResponse struct {
	Msg   string `json:"msg"`
	Error string `json:"error"`
}

type ConnectionResult struct {
	Conn         net.Conn // raw socket
	SSHConn      net.Conn // reader positioned at SSH banner
	Fingerprint  string   // from kv["fp"]
	ClientConfig *ssh.ClientConfig
}

// SenderInfo contains optional metadata about the sender advertised in hello
type SenderInfo struct {
	Keepalive int    `json:"keepalive,omitempty"`
	Identity  string `json:"identity,omitempty"`
}

// --- Entry point ---

func ConnectAndHandshake(relayAddr, code string, senderKASeconds int, senderIdentity string, token string) (*ConnectionResult, error) {
	// Parse code to separate relay code from local secret
	relayCode, _, fullCode, _ := usercode.ParseUserCode(code)

	// 1) Connect (with timeout)
	sock, err := net.DialTimeout("tcp", relayAddr, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect relay: %w", err)
	}

	// Set a deadline for the entire handshake phase (send hello + read response)
	_ = sock.SetDeadline(time.Now().Add(20 * time.Second))

	// 2) Send version + JSON hello (only relay code to relay)
	if _, err := fmt.Fprintln(sock, "ssh-relay/1.0"); err != nil {
		sock.Close()
		return nil, fmt.Errorf("send version: %w", err)
	}
	hello := JSONHello{Msg: "hello", Role: "sender", Code: relayCode}
	// Attach optional token
	if token != "" {
		hello.Token = token
	}
	// Attach optional sender metadata
	if senderKASeconds > 0 || senderIdentity != "" {
		// Encode identity as base64 to avoid JSON issues with special characters
		encodedIdentity := ""
		if senderIdentity != "" {
			encodedIdentity = base64.StdEncoding.EncodeToString([]byte(senderIdentity))
		}
		hello.Sender = &SenderInfo{Keepalive: senderKASeconds, Identity: encodedIdentity}
	}
	if err := json.NewEncoder(sock).Encode(hello); err != nil {
		sock.Close()
		return nil, fmt.Errorf("send hello: %w", err)
	}
	log.Printf("Sent hello to relay at %s", relayAddr)

	// 3) Read JSON ok, then a blank line; leave SSH banner buffered
	br := bufio.NewReader(sock)
	line, err := br.ReadString('\n')
	if err != nil {
		sock.Close()
		return nil, fmt.Errorf("read ok: %w", err)
	}
	if debugProtocol && line != "" {
		fmt.Fprintf(os.Stderr, "\n=== Relay JSON Response ===\n%s=== END ===\n\n", line)
	}
	var ok JSONOKResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &ok); err != nil {
		sock.Close()
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if ok.Msg != "ok" {
		// Try to decode error response for a better message
		var er JSONErrorResponse
		_ = json.Unmarshal([]byte(strings.TrimSpace(line)), &er)
		sock.Close()
		if er.Error != "" {
			return nil, fmt.Errorf("relay error: %s", er.Error)
		}
		return nil, fmt.Errorf("unexpected response: %s", strings.TrimSpace(line))
	}

	// Expect a single blank line before SSH banner
	blank, err := br.ReadString('\n')
	if err != nil {
		sock.Close()
		return nil, fmt.Errorf("read blank line: %w", err)
	}
	if strings.TrimSpace(blank) != "" {
		sock.Close()
		return nil, fmt.Errorf("expected blank line before SSH banner")
	}

	fp := strings.TrimSpace(ok.FP)
	if fp == "" {
		sock.Close()
		return nil, fmt.Errorf("missing required key: fp")
	}

	// Optional: exp + alg
	// if expStr := fmt.Sprintf("%d", ok.Exp); expStr != "" {
	// 	sec, err := strconv.ParseInt(expStr, 10, 64)
	// 	if err != nil {
	// 		sock.Close()
	// 		return nil, fmt.Errorf("bad exp: %w", err)
	// 	}
	// 	exp := time.Unix(sec, 0)
	// 	if time.Now().After(exp.Add(clockSkew)) {
	// 		sock.Close()
	// 		return nil, fmt.Errorf("relay token expired at %s", exp.UTC().Format(time.RFC3339))
	// 	}
	// }

	// Clear the handshake deadline before SSH takes over
	_ = sock.SetDeadline(time.Time{})

	// 5) Build a connection that starts reading exactly at the SSH banner
	sshConn := &prebufConn{Conn: sock, r: io.MultiReader(br, sock)}

	// 6) Create pinned SSH client config
	// Username: relayCode only, Password: full code (relayCode-localSecret)
	cfg := &ssh.ClientConfig{
		User: relayCode,
		Auth: []ssh.AuthMethod{ssh.Password(fullCode)},
		HostKeyCallback: func(host string, addr net.Addr, key ssh.PublicKey) error {
			got := ssh.FingerprintSHA256(key)
			if got != fp {
				return fmt.Errorf("host key mismatch: got %s, want %s", got, fp)
			}
			return nil
		},
	}

	// Optional: print nice info
	if alg := strings.TrimSpace(ok.Alg); alg != "" && ok.Exp != 0 {
		fmt.Printf("Pinned receiver fp: %s (exp %d, alg %s)\n", fp, ok.Exp, alg)
	} else if alg := strings.TrimSpace(ok.Alg); alg != "" {
		fmt.Printf("Pinned receiver fp: %s (alg %s)\n", fp, alg)
	} else if ok.Exp != 0 {
		fmt.Printf("Pinned receiver fp: %s (exp %d)\n", fp, ok.Exp)
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

// (text protocol reader removed; JSON protocol is now used)

// --- Small adapter to expose buffered bytes first, then the socket ---

type prebufConn struct {
	net.Conn
	r io.Reader
}

func (c *prebufConn) Read(p []byte) (int, error) { return c.r.Read(p) }
