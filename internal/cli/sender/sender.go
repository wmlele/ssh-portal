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

const relayTCP = "127.0.0.1:4430" // set me

// --- Tuning / limits ---
const (
	maxHeaderBytes   = 32 * 1024
	maxHeaderLines   = 200
	clockSkew        = 30 * time.Second
	maxDiscardBefore = 64 * 1024 // how many bytes we'll discard while searching for "SSH-"
)

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
	return &SyncToBannerReader{src: r, searchBuf: make([]byte, 0, 1024)}
}

func (s *SyncToBannerReader) Read(p []byte) (int, error) {
	if s.synced {
		return s.src.Read(p)
	}
	tmp := make([]byte, len(p))
	n, err := s.src.Read(tmp)
	if n > 0 {
		s.searchBuf = append(s.searchBuf, tmp[:n]...)
		// Look for "SSH-" in the accumulated buffer.
		if idx := bytes.Index(s.searchBuf, []byte("SSH-")); idx >= 0 {
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

// --- Header parsing ---

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

// Reads a header block terminated by a blank line. Accepts "Key: value" or "key=value",
// tolerates a standalone "OK" line. Stops early if the buffered data already begins with "SSH-".
func readHeaderBlock(br *bufio.Reader) (textproto.MIMEHeader, error) {
	h := make(textproto.MIMEHeader)
	tp := textproto.NewReader(br)

	total, lines := 0, 0

	for {
		// If upcoming buffered bytes already start with SSH-, we're done with headers.
		if n := br.Buffered(); n > 0 {
			if peek, _ := br.Peek(n); bytes.HasPrefix(peek, []byte("SSH-")) {
				return h, nil
			}
		}

		line, err := tp.ReadLine()
		if err != nil {
			return nil, fmt.Errorf("preface read: %w", err)
		}
		lines++
		total += len(line) + 1
		if total > maxHeaderBytes || lines > maxHeaderLines {
			return nil, fmt.Errorf("control preface too large")
		}

		s := strings.TrimSpace(line)
		if s == "" {
			// blank line = end of header block
			return h, nil
		}
		if strings.EqualFold(s, "OK") {
			h.Set("Ok", "true")
			continue
		}

		if i := strings.IndexByte(s, '='); i >= 0 {
			k := canonKey(s[:i])
			v := strings.TrimSpace(s[i+1:])
			h.Add(k, v)
			continue
		}
		// Unknown line: keep for debugging
		h.Add("x-line", s)
	}
}

func parseUnixExpiry(exp string) (time.Time, error) {
	sec, err := strconv.ParseInt(strings.TrimSpace(exp), 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(sec, 0), nil
}

// --- Main client ---

func startSSHClient(code string) {
	// 1) Connect
	sock, err := net.Dial("tcp", relayTCP)
	if err != nil {
		log.Fatal(err)
	}

	// 2) HELLO
	fmt.Fprintf(sock, "HELLO sender code=%s\n", code)

	// 3) Read header block (no assumptions about overread)
	br := bufio.NewReader(sock)
	hdrs, err := readHeaderBlock(br)
	if err != nil {
		log.Fatalf("control preface error: %v", err)
	}

	// 4) Extract fields
	if proto := first(hdrs, "Proto"); proto != "" {
		log.Printf("relay proto: %s", proto)
	}
	fp := first(hdrs, "Fp", "Fingerprint", "Ok-Fp") // keep legacy-friendly
	if fp == "" {
		for k, vs := range hdrs {
			log.Printf("header %q = %q", k, strings.Join(vs, ", "))
		}
		log.Fatal("missing fp")
	}

	// Optional expiry
	if expStr := first(hdrs, "Exp"); expStr != "" {
		if exp, err := parseUnixExpiry(expStr); err == nil {
			if time.Now().After(exp.Add(clockSkew)) {
				log.Fatalf("relay token expired at %s", exp.UTC().Format(time.RFC3339))
			}
			// print both to confirm what we pinned
			fmt.Printf("Pinned receiver fp: %s (exp %d)\n", fp, exp.Unix())
		} else {
			log.Fatalf("bad Exp value: %v", err)
		}
	} else {
		fmt.Println("Pinned receiver fp:", fp)
	}

	// 5) Build a reader that:
	//    - feeds any bytes already buffered in br first,
	//    - then the socket,
	//    - and synchronizes to the SSH banner before exposing data to ssh.NewClientConn.
	var pre []byte
	if n := br.Buffered(); n > 0 {
		pre = make([]byte, n)
		if _, err := io.ReadFull(br, pre); err != nil {
			log.Fatal(err)
		}
	}
	upstream := io.Reader(sock)
	if len(pre) > 0 {
		upstream = io.MultiReader(bytes.NewReader(pre), sock)
	}
	syncReader := NewSyncToBannerReader(upstream)

	wsock := &prebufConn{Conn: sock, r: syncReader}

	// 6) SSH handshake (pinned host key)
	cfg := &ssh.ClientConfig{
		User: "ignored",
		Auth: []ssh.AuthMethod{ssh.Password("ignored")},
		HostKeyCallback: func(host string, addr net.Addr, key ssh.PublicKey) error {
			got := ssh.FingerprintSHA256(key)
			if got != fp {
				return fmt.Errorf("host key mismatch: %s != %s", got, fp)
			}
			return nil
		},
		// Timeout: 10 * time.Second,
	}

	cc, chans, reqs, err := ssh.NewClientConn(wsock, "paired", cfg)
	if err != nil {
		log.Fatal(err)
	}
	client := ssh.NewClient(cc, chans, reqs)
	defer client.Close()

	// 7) Example local forward
	go localForward(client, "127.0.0.1:10022", "127.0.0.1:22")
	fmt.Println("try: ssh -p 10022 localhost")

	// 8) Interactive shell
	if err := interactiveShell(client); err != nil {
		log.Fatal(err)
	}
}

func localForward(c *ssh.Client, listen, target string) {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		log.Println("listen:", err)
		return
	}
	defer ln.Close()
	log.Println("local forward listening on", listen, "->", target)
	for {
		lc, err := ln.Accept()
		if err != nil {
			return
		}
		go func(lc net.Conn) {
			rc, err := c.Dial("tcp", target)
			if err != nil {
				lc.Close()
				return
			}
			go io.Copy(rc, lc)
			go func() { io.Copy(lc, rc); lc.Close(); rc.Close() }()
		}(lc)
	}
}

func interactiveShell(c *ssh.Client) error {
	sess, err := c.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	_ = sess.RequestPty("xterm-256color", 40, 120, ssh.TerminalModes{})
	sess.Stdin = os.Stdin
	sess.Stdout = os.Stdout
	sess.Stderr = os.Stderr
	return sess.Shell()
}

// Run executes the sender command
func Run(code string) error {
	if code == "" {
		return fmt.Errorf("code is required")
	}
	startSSHClient(code)
	return nil
}
