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

// HelloMessage represents a parsed HELLO message
type HelloMessage struct {
	Side string // "sender" or "receiver"
	Code string // for sender
	RID  string // for receiver
}

// ParseHelloMessage parses a HELLO message from the connection
func ParseHelloMessage(c net.Conn) (*HelloMessage, error) {
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	defer c.SetDeadline(time.Time{}) // clear deadline

	br := bufio.NewReader(c)
	line, err := br.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read error: %w", err)
	}

	line = strings.TrimSpace(line)
	log.Printf("[TCP] %s -> received: %s", c.RemoteAddr(), line)

	parts := strings.Split(line, " ")
	if len(parts) < 3 || parts[0] != "HELLO" {
		return nil, fmt.Errorf("invalid message format")
	}

	msg := &HelloMessage{Side: parts[1]}

	switch msg.Side {
	case "receiver":
		msg.RID = extractValue(parts[2], "rid=")
	case "sender":
		msg.Code = extractValue(parts[2], "code=")
	default:
		return nil, fmt.Errorf("unknown side: %s", msg.Side)
	}

	return msg, nil
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
//
//	OK
//
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
func HandleReceiver(c net.Conn, rid string) *Invite {
	remoteAddr := c.RemoteAddr().String()
	log.Printf("[TCP] %s -> receiver connecting with rid=%s", remoteAddr, rid)

	inv := getByRID(rid)
	if inv == nil || time.Now().After(inv.ExpiresAt) {
		log.Printf("[TCP] %s -> ERR: invalid or expired rid=%s", remoteAddr, rid)
		SendErrorResponse(c, "no-invite")
		c.Close()
		return nil
	}

	// Check if receiver already attached
	invMu.Lock()
	if inv.ReceiverConn != nil {
		invMu.Unlock()
		log.Printf("[TCP] %s -> ERR: receiver already attached for rid=%s", remoteAddr, rid)
		SendErrorResponse(c, "already-attached")
		c.Close()
		return nil
	}
	inv.ReceiverConn = c
	invMu.Unlock()

	log.Printf("[TCP] %s -> receiver attached successfully: code=%s rid=%s waiting for sender...", remoteAddr, inv.Code, rid)

	// Wait for sender or timeout
	go func() {
		<-time.After(time.Until(inv.ExpiresAt))
		log.Printf("[TCP] %s -> receiver connection timeout/closed", remoteAddr)
	}()

	return inv
}

// ====== Sender protocol handler ======

// HandleSender processes a sender connection
// Returns the invite if ready for pairing, nil on error
func HandleSender(c net.Conn, code string) *Invite {
	remoteAddr := c.RemoteAddr().String()
	log.Printf("[TCP] %s -> sender connecting with code=%s", remoteAddr, code)

	inv := getByCode(code)
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
