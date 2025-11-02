package sender

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"net/textproto"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// --- Tuning / limits ---
const (
	maxHeaderBytes   = 32 * 1024
	maxHeaderLines   = 200
	clockSkew        = 30 * time.Second
	maxDiscardBefore = 64 * 1024 // how many bytes we'll discard while searching for "SSH-"
)

// --- Debug flag ---
// Set SSH_PORTAL_DEBUG environment variable to enable debug output of raw relay responses
var debugProtocol = true //os.Getenv("SSH_PORTAL_DEBUG") != ""

// --- Protocol response structures ---

// ProtocolResponse represents a parsed relay protocol response
type ProtocolResponse struct {
	Proto string
	OK    bool
	Err   string
	FP    string
	Exp   int64
	Alg   string
}

// --- Protocol communication ---

// ConnectionResult holds the result of connecting and handshaking with the relay
type ConnectionResult struct {
	Conn         net.Conn
	SSHConn      net.Conn
	Fingerprint  string
	ClientConfig *ssh.ClientConfig
}

// ConnectAndHandshake connects to the relay, performs the protocol handshake,
// and prepares the connection for SSH. Returns the connection result or an error.
func ConnectAndHandshake(relayAddr, code string) (*ConnectionResult, error) {
	// 1) Connect to relay
	sock, err := net.Dial("tcp", relayAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to relay: %w", err)
	}

	// 2) Send HELLO message
	if _, err := fmt.Fprintf(sock, "HELLO sender code=%s\n", code); err != nil {
		sock.Close()
		return nil, fmt.Errorf("failed to send HELLO: %w", err)
	}
	log.Println("HELLO sent")

	// 3) Read and parse protocol response
	br := bufio.NewReader(sock)
	hdrs, rawResponse, err := readHeaderBlock(br)
	if err != nil {
		sock.Close()
		return nil, fmt.Errorf("control preface error: %w", err)
	}
	log.Println("Header block read")

	// Debug: print raw response if enabled
	if debugProtocol {
		fmt.Fprintf(os.Stderr, "\n=== DEBUG: Relay Response ===\n%s=== END DEBUG ===\n\n", rawResponse)
	}

	resp, err := ParseProtocolResponse(hdrs)
	if err != nil {
		sock.Close()
		return nil, fmt.Errorf("failed to parse protocol response: %w", err)
	}

	// 4) Validate and process response
	if err := validateProtocolResponse(resp, hdrs); err != nil {
		sock.Close()
		return nil, err
	}

	printConnectionInfo(resp)

	// 5) Prepare SSH connection with banner synchronization
	sshConn, err := prepareSSHConnection(sock, br)
	if err != nil {
		sock.Close()
		return nil, fmt.Errorf("failed to prepare SSH connection: %w", err)
	}

	log.Println("SSH connection prepared")

	// 6) Create SSH client config with pinned host key
	cfg := createSSHConfig(resp.FP)

	return &ConnectionResult{
		Conn:         sock,
		SSHConn:      sshConn,
		Fingerprint:  resp.FP,
		ClientConfig: cfg,
	}, nil
}

// --- Protocol response parsing ---

// ParseProtocolResponse parses the header block into a ProtocolResponse
func ParseProtocolResponse(h textproto.MIMEHeader) (*ProtocolResponse, error) {
	resp := &ProtocolResponse{}

	// Check protocol version
	resp.Proto = first(h, "Proto")
	if resp.Proto == "" {
		return nil, fmt.Errorf("missing protocol version")
	}

	// Check if OK or ERR
	okVal := first(h, "Ok")
	if strings.EqualFold(okVal, "true") {
		resp.OK = true
	} else {
		resp.OK = false
		// Try to find ERR message
		errMsg := first(h, "Err", "Error")
		if errMsg == "" {
			// Check if there's an ERR line in x-line fields
			for _, line := range h.Values("x-line") {
				if strings.HasPrefix(strings.ToUpper(line), "ERR") {
					parts := strings.SplitN(line, " ", 2)
					if len(parts) > 1 {
						errMsg = parts[1]
					} else {
						errMsg = "unknown error"
					}
					break
				}
			}
		}
		resp.Err = errMsg
	}

	// Extract fields
	resp.FP = first(h, "Fp", "Fingerprint", "Ok-Fp")
	if expStr := first(h, "Exp"); expStr != "" {
		if exp, err := parseUnixExpiry(expStr); err == nil {
			resp.Exp = exp.Unix()
		}
	}
	resp.Alg = first(h, "Alg", "Algorithm")

	return resp, nil
}

// validateProtocolResponse validates the protocol response and returns an error if invalid
func validateProtocolResponse(resp *ProtocolResponse, hdrs textproto.MIMEHeader) error {
	// Check protocol version
	if resp.Proto != "ssh-relay/1" {
		return fmt.Errorf("unsupported protocol version: %s", resp.Proto)
	}

	// Handle ERR responses
	if !resp.OK {
		errMsg := resp.Err
		if errMsg == "" {
			errMsg = "unknown error"
		}
		return fmt.Errorf("relay error: %s", errMsg)
	}

	// Validate required fields
	if resp.FP == "" {
		for k, vs := range hdrs {
			fmt.Printf("header %q = %q\n", k, strings.Join(vs, ", "))
		}
		return fmt.Errorf("missing fp")
	}

	// Optional expiry check
	if resp.Exp > 0 {
		exp := time.Unix(resp.Exp, 0)
		if time.Now().After(exp.Add(clockSkew)) {
			return fmt.Errorf("relay token expired at %s", exp.UTC().Format(time.RFC3339))
		}
	}

	return nil
}

// printConnectionInfo prints connection information to stdout
func printConnectionInfo(resp *ProtocolResponse) {
	if resp.Exp > 0 {
		if resp.Alg != "" {
			fmt.Printf("Pinned receiver fp: %s (exp %d, alg %s)\n", resp.FP, resp.Exp, resp.Alg)
		} else {
			fmt.Printf("Pinned receiver fp: %s (exp %d)\n", resp.FP, resp.Exp)
		}
	} else {
		if resp.Alg != "" {
			fmt.Printf("Pinned receiver fp: %s (alg %s)\n", resp.FP, resp.Alg)
		} else {
			fmt.Println("Pinned receiver fp:", resp.FP)
		}
	}
}

// --- Header parsing utilities ---

func canonKey(k string) string {
	k = strings.TrimSpace(k)
	k = strings.ReplaceAll(k, " ", "-")
	return textproto.CanonicalMIMEHeaderKey(k)
}

func first(h textproto.MIMEHeader, keys ...string) string {
	for _, k := range keys {
		if v := h.Get(canonKey(k)); v != "" {
			return v
		}
	}
	return ""
}

// readHeaderBlock reads a header block terminated by SSH banner.
// The protocol format uses blank lines as separators between sections:
//
//	proto: ssh-relay/1
//	[blank]
//	OK/ERR
//	[blank]
//	key=value lines (optional)
//	[blank]
//	SSH banner
//
// Accepts "Key: value" or "key=value", tolerates a standalone "OK" or "ERR" line.
// Continues reading until we detect SSH banner in the buffer, then stops.
// Returns the parsed headers and the raw response text (for debugging).
func readHeaderBlock(br *bufio.Reader) (textproto.MIMEHeader, string, error) {
	h := make(textproto.MIMEHeader)
	tp := textproto.NewReader(br)

	var rawLines []string
	total, lines := 0, 0

	for {
		// Check if upcoming buffered bytes start with SSH banner
		if n := br.Buffered(); n > 0 {
			peek, _ := br.Peek(min(n, 4)) // Only peek at first 4 bytes to check for "SSH-"
			if bytes.HasPrefix(peek, []byte("SSH-")) {
				// SSH banner found - return what we've read so far
				rawResponse := ""
				if len(rawLines) > 0 {
					rawResponse = strings.Join(rawLines, "\n") + "\n"
				}
				return h, rawResponse, nil
			}
		}

		// Try to read a line
		line, err := tp.ReadLine()
		if err != nil {
			// On EOF, return what we have (this is normal for ERR responses without SSH banner)
			if err == io.EOF {
				if len(rawLines) > 0 {
					rawResponse := strings.Join(rawLines, "\n") + "\n"
					return h, rawResponse, nil
				}
				return h, "", nil // Empty response
			}
			// Other errors
			if len(rawLines) > 0 {
				rawResponse := strings.Join(rawLines, "\n") + "\n"
				return h, rawResponse, nil
			}
			return nil, "", fmt.Errorf("preface read: %w", err)
		}

		lines++
		total += len(line) + 1
		if total > maxHeaderBytes || lines > maxHeaderLines {
			return nil, "", fmt.Errorf("control preface too large")
		}

		// Capture raw line for debug output
		rawLines = append(rawLines, string(line))

		// Check if this line contains SSH banner (unlikely but possible)
		if bytes.Contains([]byte(line), []byte("SSH-")) {
			// Found SSH banner in the line - stop here
			rawResponse := strings.Join(rawLines, "\n") + "\n"
			return h, rawResponse, nil
		}

		s := strings.TrimSpace(line)
		if s == "" {
			// Blank line - continue reading (protocol uses blanks as separators)
			continue
		}

		// Parse non-blank lines
		if strings.EqualFold(s, "OK") {
			h.Set("Ok", "true")
			continue
		}

		// Handle ERR lines
		if strings.HasPrefix(strings.ToUpper(s), "ERR") {
			parts := strings.SplitN(s, " ", 2)
			if len(parts) > 1 {
				h.Set("Err", parts[1])
			} else {
				h.Set("Err", "unknown error")
			}
			h.Set("Ok", "false")
			continue
		}

		if i := strings.IndexByte(s, '='); i >= 0 {
			k := canonKey(s[:i])
			v := strings.TrimSpace(s[i+1:])
			h.Add(k, v)
			continue
		}

		// Handle "key: value" format
		if i := strings.IndexByte(s, ':'); i >= 0 {
			k := canonKey(s[:i])
			v := strings.TrimSpace(s[i+1:])
			h.Add(k, v)
			continue
		}

		// Unknown line: keep for debugging
		h.Add("x-line", s)
	}
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func parseUnixExpiry(exp string) (time.Time, error) {
	sec, err := strconv.ParseInt(strings.TrimSpace(exp), 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(sec, 0), nil
}

// --- SSH banner synchronization ---

// prebufConn feeds r first, then the underlying Conn.
type prebufConn struct {
	net.Conn
	r io.Reader
}

func (c *prebufConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// SyncToBannerReader discards everything until it encounters "SSH-",
// then returns data starting exactly at the banner and passes through thereafter.
type SyncToBannerReader struct {
	src       io.Reader
	synced    bool
	searchBuf []byte
	discarded int
}

func NewSyncToBannerReader(r io.Reader) *SyncToBannerReader {
	return &SyncToBannerReader{src: r, searchBuf: make([]byte, 0, 2048)}
}

func (s *SyncToBannerReader) Read(p []byte) (int, error) {
	if s.synced {
		return s.src.Read(p)
	}

	log.Println("SyncToBannerReader: Read", len(p))

	tmp := make([]byte, len(p))
	n, err := s.src.Read(tmp)

	log.Println("SyncToBannerReader: Read", n)

	if n > 0 {
		s.searchBuf = append(s.searchBuf, tmp[:n]...)
		// Look for "SSH-" in the accumulated buffer.
		if idx := bytes.Index(s.searchBuf, []byte("SSH-")); idx >= 0 {
			log.Println("SyncToBannerReader: Found SSH banner at offset", idx)
			s.synced = true
			// Discard bytes before the banner.
			pre := s.searchBuf[:idx]
			s.discarded += len(pre)
			if s.discarded > maxDiscardBefore {
				return 0, fmt.Errorf("too much preface before SSH banner")
			}
			// Return from the banner onward.
			out := s.searchBuf[idx:]
			copied := copy(p, out)
			// Keep any remainder for next Read.
			s.searchBuf = s.searchBuf[idx+copied:]
			log.Println("SyncToBannerReader: Returning", copied, "bytes")
			return copied, nil
		}
		// Not found yet; enforce limit
		if len(s.searchBuf) > maxDiscardBefore {
			return 0, fmt.Errorf("no SSH banner found within %d bytes", maxDiscardBefore)
		}
		// Keep reading until we can find the banner. We don't return data yet.
		return 0, nil
	}
	// Propagate EOF/err (but if we never synced, surface a helpful error)
	if err == io.EOF && !s.synced {
		return 0, fmt.Errorf("connection closed before SSH banner")
	}
	return n, err
}

// prepareSSHConnection builds a reader that synchronizes to the SSH banner
// and wraps it in a connection-like interface.
func prepareSSHConnection(sock net.Conn, br *bufio.Reader) (net.Conn, error) {
	// Extract any buffered bytes
	var pre []byte
	if n := br.Buffered(); n > 0 {
		pre = make([]byte, n)
		if _, err := io.ReadFull(br, pre); err != nil {
			return nil, fmt.Errorf("failed to read buffered bytes: %w", err)
		}
	}

	// Build upstream reader
	upstream := io.Reader(sock)
	if len(pre) > 0 {
		upstream = io.MultiReader(bytes.NewReader(pre), sock)
	}

	// Synchronize to SSH banner
	syncReader := NewSyncToBannerReader(upstream)

	// Wrap in connection-like interface
	return &prebufConn{Conn: sock, r: syncReader}, nil
}

// createSSHConfig creates an SSH client configuration with pinned host key
func createSSHConfig(expectedFP string) *ssh.ClientConfig {
	return &ssh.ClientConfig{
		User: "ignored",
		Auth: []ssh.AuthMethod{ssh.Password("ignored")},
		HostKeyCallback: func(host string, addr net.Addr, key ssh.PublicKey) error {
			got := ssh.FingerprintSHA256(key)
			if got != expectedFP {
				return fmt.Errorf("host key mismatch: %s != %s", got, expectedFP)
			}
			return nil
		},
	}
}
