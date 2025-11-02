package relay

import (
	"bufio"
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

// HelloMessage represents a parsed HELLO message
type HelloMessage struct {
	Side string // "sender" or "receiver"
	Code string // for sender
	RID  string // for receiver
}

// ParseHelloMessage parses a HELLO message from the connection
// Returns the parsed message and a buffered reader containing any remaining data
// The buffered reader must be used for further reads to preserve any data sent after HELLO
func ParseHelloMessage(c net.Conn) (*HelloMessage, *bufio.Reader, error) {
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	defer c.SetDeadline(time.Time{}) // clear deadline

	br := bufio.NewReader(c)
	line, err := br.ReadString('\n')
	if err != nil {
		return nil, nil, fmt.Errorf("read error: %w", err)
	}

	line = strings.TrimSpace(line)
	log.Printf("[TCP] %s -> received: %s", c.RemoteAddr(), line)

	parts := strings.Split(line, " ")
	if len(parts) < 3 || parts[0] != "HELLO" {
		return nil, nil, fmt.Errorf("invalid message format")
	}

	msg := &HelloMessage{Side: parts[1]}

	switch msg.Side {
	case "receiver":
		msg.RID = extractValue(parts[2], "rid=")
	case "sender":
		msg.Code = extractValue(parts[2], "code=")
	default:
		return nil, nil, fmt.Errorf("unknown side: %s", msg.Side)
	}

	return msg, br, nil
}

func extractValue(s, prefix string) string {
	if strings.HasPrefix(s, prefix) {
		return strings.TrimPrefix(s, prefix)
	}
	return ""
}

// ====== Protocol response helpers ======

// ProtocolResponse represents a relay protocol response
type ProtocolResponse struct {
	Proto string
	OK    bool
	Err   string
	FP    string
	Exp   int64
	Alg   string
}

// ComposeResponseHeader builds the protocol header block according to:
//
//	proto: ssh-relay/1
//	OK
//	fp=SHA256:...
//	exp=1730550000
//	alg=ssh-ed25519
//
//	(blank line before SSH banner)
func ComposeResponseHeader(resp ProtocolResponse) string {
	var sb strings.Builder

	// Protocol version line
	sb.WriteString("proto: ssh-relay/1\n")

	// OK or ERR line
	if resp.OK {
		sb.WriteString("OK\n")
	} else {
		sb.WriteString("ERR")
		if resp.Err != "" {
			sb.WriteString(" ")
			sb.WriteString(resp.Err)
		}
		sb.WriteString("\n")
	}

	// Fields (key=value format)
	if resp.FP != "" {
		sb.WriteString("fp=")
		sb.WriteString(resp.FP)
		sb.WriteString("\n")
	}

	if resp.Exp > 0 {
		sb.WriteString("exp=")
		sb.WriteString(fmt.Sprintf("%d", resp.Exp))
		sb.WriteString("\n")
	}

	if resp.Alg != "" {
		sb.WriteString("alg=")
		sb.WriteString(resp.Alg)
		sb.WriteString("\n")
	}

	sb.WriteString("\n") // final blank line before SSH banner
	return sb.String()
}

// SendErrorResponse sends an error response to the connection
func SendErrorResponse(c net.Conn, errMsg string) error {
	resp := ProtocolResponse{OK: false, Err: errMsg}
	header := ComposeResponseHeader(resp)
	_, err := c.Write([]byte(header))
	if err != nil {
		log.Printf("[TCP] write error: %v", err)
	}
	return err
}

// SendSuccessResponse sends a success response with fingerprint and expiry
func SendSuccessResponse(c net.Conn, fp string, exp int64, alg string) error {
	resp := ProtocolResponse{
		OK:  true,
		FP:  fp,
		Exp: exp,
		Alg: alg,
	}
	header := ComposeResponseHeader(resp)
	_, err := c.Write([]byte(header))
	if err != nil {
		log.Printf("[TCP] write error: %v", err)
	}
	return err
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
func HandleSender(c net.Conn, code string) *Invite {
	remoteAddr := c.RemoteAddr().String()
	log.Printf("[TCP] %s -> sender connecting with code=%s", remoteAddr, code)

	inv := GetByCode(code)
	if inv == nil || time.Now().After(inv.ExpiresAt) || inv.ReceiverConn == nil {
		log.Printf("[TCP] %s -> ERR: code %s not ready (invalid/expired/no receiver)", remoteAddr, code)
		SendErrorResponse(c, "not-ready")
		c.Close()
		return nil
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
