package relay

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
	"time"
)

// ====== Protocol message parsing ======

// bufferedConn wraps a net.Conn with a bufio.Reader to preserve buffered data
type bufferedConn struct {
	net.Conn
	br *bufio.Reader
}

func newBufferedConn(c net.Conn, br *bufio.Reader) *bufferedConn {
	return &bufferedConn{Conn: c, br: br}
}

func (bc *bufferedConn) Read(p []byte) (int, error) {
	return bc.br.Read(p)
}

// JSON protocol messages
type EndpointMessage struct {
	Msg        string      `json:"msg"`  // "hello", "await"
	Role       string      `json:"role"` // "sender" or "receiver"
	Code       string      `json:"code,omitempty"`
	RID        string      `json:"rid,omitempty"`
	ReceiverFP string      `json:"receiver_fp,omitempty"`
	TTLSeconds int         `json:"ttl_seconds,omitempty"`
	Token      string      `json:"token,omitempty"`
	Sender     *SenderInfo `json:"sender,omitempty"`
}

type OKResponse struct {
	Msg string `json:"msg"` // "ok"
	FP  string `json:"fp,omitempty"`
	Exp int64  `json:"exp,omitempty"`
	Alg string `json:"alg,omitempty"`
}

type ErrorResponse struct {
	Msg string `json:"msg"` // "error"
	Err string `json:"error"`
}

// HelloOKResponse is sent back to a receiver after a successful hello
type HelloOKResponse struct {
	Msg  string `json:"msg"` // "hello_ok"
	Code string `json:"code"`
	RID  string `json:"rid"`
	Exp  int64  `json:"exp"`
}

// ReadyMessage is sent to receiver when sender connects
type ReadyMessage struct {
	Msg         string      `json:"msg"` // "ready"
	SenderAddr  string      `json:"sender_addr"`
	Fingerprint string      `json:"fp"`
	Exp         int64       `json:"exp"`
	Alg         string      `json:"alg,omitempty"`
	Sender      *SenderInfo `json:"sender,omitempty"`
}

// SenderInfo mirrors the sender metadata provided in the initial hello
type SenderInfo struct {
	Keepalive int    `json:"keepalive,omitempty"`
	Identity  string `json:"identity,omitempty"`
}

// ParseMessage reads the version line and a single JSON payload message.
// It returns the parsed EndpointMessage and a buffered reader preserving any extra bytes.
func ParseMessage(c net.Conn) (*EndpointMessage, *bufio.Reader, error) {
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	defer c.SetDeadline(time.Time{}) // clear deadline

	br := bufio.NewReader(c)

	// 1) Expect exact version line
	verLine, err := br.ReadString('\n')
	if err != nil {
		return nil, nil, fmt.Errorf("read version: %w", err)
	}
	if strings.TrimSpace(verLine) != "ssh-relay/1.0" {
		return nil, nil, fmt.Errorf("unsupported or missing version line")
	}

	// 2) Read one JSON line
	jsonLine, err := br.ReadString('\n')
	if err != nil {
		return nil, nil, fmt.Errorf("read payload json: %w", err)
	}
	jsonLine = strings.TrimSpace(jsonLine)
	log.Printf("[TCP] %s -> payload json: %s", c.RemoteAddr(), jsonLine)

	var payload EndpointMessage
	if err := json.Unmarshal([]byte(jsonLine), &payload); err != nil {
		return nil, nil, fmt.Errorf("decode payload: %w", err)
	}
	// validate based on role and message
	if payload.Role != "sender" && payload.Role != "receiver" {
		return nil, nil, fmt.Errorf("invalid role %q", payload.Role)
	}
	if payload.Msg == "hello" && payload.Role == "sender" && payload.Code == "" {
		return nil, nil, fmt.Errorf("missing code for sender")
	}
	if payload.Msg == "await" && payload.Role == "receiver" && payload.RID == "" {
		return nil, nil, fmt.Errorf("missing rid for receiver")
	}
	if payload.Msg == "hello" && payload.Role == "receiver" && payload.ReceiverFP == "" {
		return nil, nil, fmt.Errorf("invalid hello message (need receiver_fp)")
	}

	return &payload, br, nil
}

// ====== JSON response helpers ======

func sendJSON(c net.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if _, err := c.Write(b); err != nil {
		return err
	}
	return nil
}

// SendErrorResponse sends a JSON error response
func SendErrorResponse(c net.Conn, errMsg string) error {
	return sendJSON(c, ErrorResponse{Msg: "error", Err: errMsg})
}

// SendSuccessResponse sends a JSON ok response and a blank line before SSH starts
func SendSuccessResponse(c net.Conn, fp string, exp int64, alg string) error {
	if err := sendJSON(c, OKResponse{Msg: "ok", FP: fp, Exp: exp, Alg: alg}); err != nil {
		return err
	}
	// Single blank line before SSH banner begins
	if _, err := c.Write([]byte("\n")); err != nil {
		return err
	}
	return nil
}

// ====== Receiver protocol handler ======

// HandleReceiver processes a receiver connection
// Returns the invite if successfully attached, nil on error
// The returned connection should be a bufferedConn to preserve any SSH banner data
func HandleReceiver(c net.Conn, rid string, br *bufio.Reader) (*Invite, net.Conn) {
	remoteAddr := c.RemoteAddr().String()
	log.Printf("[TCP] %s -> receiver connecting with rid=%s", remoteAddr, rid)

	inv := GetByRID(rid)
	if inv == nil || time.Now().After(inv.ExpiresAt) {
		log.Printf("[TCP] %s -> ERR: invalid or expired rid=%s", remoteAddr, rid)
		SendErrorResponse(c, "no-invite")
		c.Close()
		return nil, nil
	}

	// Check if receiver already attached
	LockInvites()
	if inv.ReceiverConn != nil {
		UnlockInvites()
		log.Printf("[TCP] %s -> ERR: receiver already attached for rid=%s", remoteAddr, rid)
		SendErrorResponse(c, "already-attached")
		c.Close()
		return nil, nil
	}

	// Wrap connection with buffered reader to preserve any SSH banner data
	bufferedC := newBufferedConn(c, br)
	inv.ReceiverConn = bufferedC
	UnlockInvites()

	log.Printf("[TCP] %s -> receiver attached successfully: code=%s rid=%s waiting for sender...", remoteAddr, inv.Code, rid)

	// Wait for sender or timeout
	go func() {
		<-time.After(time.Until(inv.ExpiresAt))
		log.Printf("[TCP] %s -> receiver connection timeout/closed", remoteAddr)
	}()

	return inv, bufferedC
}

// ====== Sender protocol handler ======

// HandleSender processes a sender connection
// Returns the invite if ready for pairing, nil on error
func HandleSender(c net.Conn, code string, meta *SenderInfo) *Invite {
	remoteAddr := c.RemoteAddr().String()
	ip, _, _ := net.SplitHostPort(remoteAddr)
	log.Printf("[TCP] %s -> sender connecting with code=%s", remoteAddr, code)

	// Throttle after repeated failures from this IP
	checkRateLimit(ip)

	inv := GetByCode(code)
	if inv == nil || time.Now().After(inv.ExpiresAt) || inv.ReceiverConn == nil {
		recordFailedAttempt(ip)
		log.Printf("[TCP] %s -> ERR: code %s not ready (invalid/expired/no receiver)", remoteAddr, code)
		SendErrorResponse(c, "not-ready")
		c.Close()
		return nil
	}

	clearFailedAttempts(ip)

	// Attach sender metadata to invite for forwarding to receiver
	if meta != nil {
		LockInvites()
		inv.Sender = meta
		UnlockInvites()
	}

	// Send authentication response if not already sent
	if !inv.sentOK {
		alg := "" // TODO: extract from receiver connection if available
		if err := SendSuccessResponse(c, inv.ReceiverFP, inv.ExpiresAt.Unix(), alg); err != nil {
			return nil
		}
		inv.sentOK = true
		log.Printf("[TCP] %s -> sender authenticated: code=%s fp=%s", remoteAddr, code, inv.ReceiverFP)
	}

	return inv
}
