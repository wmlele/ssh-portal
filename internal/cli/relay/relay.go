package relay

import (
	"bufio"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ====== invite registry ======
type Invite struct {
	RID          string
	Code         string
	ReceiverFP   string // "SHA256:..."
	ExpiresAt    time.Time
	ReceiverConn net.Conn
	sentOK       bool
}

var (
	invMu   sync.Mutex
	invByID = map[string]*Invite{}
	invByCd = map[string]*Invite{}
)

func mint(receiverFP string, ttl time.Duration) *Invite {
	rid := randB32(16)               // rendezvous id (base32)
	code := fmtCode()                // human code
	exp := time.Now().Add(ttl).UTC() // expiry
	inv := &Invite{RID: rid, Code: code, ReceiverFP: receiverFP, ExpiresAt: exp}
	invMu.Lock()
	invByID[rid] = inv
	invByCd[code] = inv
	invMu.Unlock()
	return inv
}

func getByRID(rid string) *Invite   { invMu.Lock(); defer invMu.Unlock(); return invByID[rid] }
func getByCode(code string) *Invite { invMu.Lock(); defer invMu.Unlock(); return invByCd[code] }
func cleanupLoop() {
	t := time.NewTicker(1 * time.Minute)
	for range t.C {
		now := time.Now()
		invMu.Lock()
		cleaned := 0
		for k, v := range invByID {
			if now.After(v.ExpiresAt) {
				delete(invByID, k)
				delete(invByCd, v.Code)
				if v.ReceiverConn != nil {
					log.Printf("[CLEANUP] closing expired connection: code=%s rid=%s", v.Code, v.RID)
					v.ReceiverConn.Close()
				}
				cleaned++
			}
		}
		invMu.Unlock()
		if cleaned > 0 {
			log.Printf("[CLEANUP] removed %d expired invite(s)", cleaned)
		}
	}
}

// ====== HTTP: /mint ======
type mintReq struct {
	ReceiverFP string `json:"receiver_fp"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"` // optional; default 600
}
type mintResp struct {
	Code string    `json:"code"`
	RID  string    `json:"rid"`
	Exp  time.Time `json:"exp"`
}

func handleMint(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		log.Printf("[HTTP] %s %s -> 405 Method Not Allowed", r.Method, r.URL.Path)
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req mintReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !strings.HasPrefix(req.ReceiverFP, "SHA256:") {
		log.Printf("[HTTP] %s %s -> 400 Bad Request (bad json or fp)", r.Method, r.URL.Path)
		http.Error(w, "bad json or fp", http.StatusBadRequest)
		return
	}
	ttl := 10 * time.Minute
	if req.TTLSeconds > 0 && req.TTLSeconds <= 3600 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}
	inv := mint(req.ReceiverFP, ttl)
	log.Printf("[MINT] receiver connected: fp=%s code=%s rid=%s expires=%s", req.ReceiverFP, inv.Code, inv.RID, inv.ExpiresAt.Format(time.RFC3339))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(mintResp{Code: inv.Code, RID: inv.RID, Exp: inv.ExpiresAt})
}

// ====== TCP rendezvous/splice ======
func tcpServe(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	log.Printf("relay TCP listening on %s", addr)
	for {
		c, err := ln.Accept()
		if err != nil {
			log.Printf("[TCP] accept error: %v", err)
			continue
		}
		log.Printf("[TCP] new connection from %s", c.RemoteAddr())
		go handleTCP(c)
	}
}

func handleTCP(c net.Conn) {
	remoteAddr := c.RemoteAddr().String()

	// Parse HELLO message
	msg, br, err := ParseHelloMessage(c)
	if err != nil {
		log.Printf("[TCP] %s -> %v", remoteAddr, err)
		c.Close()
		return
	}

	// Dispatch to appropriate handler
	switch msg.Side {
	case "receiver":
		handleReceiverConnection(c, msg.RID, br)
	case "sender":
		handleSenderConnection(c, msg.Code, br)
	default:
		log.Printf("[TCP] %s -> ERR: unknown side '%s'", remoteAddr, msg.Side)
		SendErrorResponse(c, "bad-side")
		c.Close()
	}
}

// handleReceiverConnection processes a receiver connection and waits for pairing
func handleReceiverConnection(c net.Conn, rid string, br *bufio.Reader) {
	inv, bufferedC := HandleReceiver(c, rid, br)
	if inv == nil {
		// Error already handled and connection closed by HandleReceiver
		return
	}
	// Connection is now attached to invite and waiting for sender
	// bufferedC is used to preserve any SSH banner data that was buffered
	_ = bufferedC // stored in inv.ReceiverConn
	// Timeout is handled by goroutine in HandleReceiver
}

// handleSenderConnection processes a sender connection and pairs with receiver
func handleSenderConnection(c net.Conn, code string, br *bufio.Reader) {
	inv := HandleSender(c, code)
	if inv == nil {
		// Error already handled and connection closed by HandleSender
		return
	}

	// Pair sender with receiver and splice connections
	// Note: rc is already a bufferedConn that preserves any SSH banner data
	rc := inv.ReceiverConn
	rcAddr := rc.RemoteAddr().String()
	senderAddr := c.RemoteAddr().String()

	// Remove from maps to make it one-shot
	invMu.Lock()
	delete(invByID, inv.RID)
	delete(invByCd, inv.Code)
	invMu.Unlock()

	log.Printf("[PAIR] successfully paired: sender=%s receiver=%s code=%s rid=%s", senderAddr, rcAddr, inv.Code, inv.RID)
	log.Printf("[SPLICE] bridging sender=%s <-> receiver=%s", senderAddr, rcAddr)
	splice(rc, c) // closes both connections; rc is bufferedConn preserving SSH banner
	log.Printf("[SPLICE] connection closed: sender=%s receiver=%s", senderAddr, rcAddr)
}

func splice(a, b net.Conn) {
	defer a.Close()
	defer b.Close()
	done := make(chan struct{}, 2)
	var aBytes, bBytes int64
	aAddr := a.RemoteAddr().String()
	bAddr := b.RemoteAddr().String()
	go func() {
		n, _ := io.Copy(a, b)
		bBytes = n
		done <- struct{}{}
	}()
	go func() {
		n, _ := io.Copy(b, a)
		aBytes = n
		done <- struct{}{}
	}()
	<-done // wait for first direction
	<-done // wait for second direction
	log.Printf("[SPLICE] stats: %s <-> %s (%d bytes a->b, %d bytes b->a)",
		aAddr, bAddr, bBytes, aBytes)
}

func randB32(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return strings.TrimRight(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), "=")
}

var words = []string{
	"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india",
	"juliet", "kilo", "lima", "mike", "november", "oscar", "papa", "quebec", "romeo",
	"sierra", "tango", "uniform", "victor", "whiskey", "xray", "yankee", "zulu",
}

func fmtCode() string {
	w1 := words[randN(len(words))]
	w2 := words[randN(len(words))]
	num := randN(9000) + 1000
	return fmt.Sprintf("%s-%s-%d", w1, w2, num)
}
func randN(n int) int {
	v, _ := rand.Int(rand.Reader, big.NewInt(int64(n)))
	return int(v.Int64())
}

// Run executes the relay command
func Run() error {
	go cleanupLoop()
	http.HandleFunc("/mint", handleMint)
	go func() {
		log.Println("relay HTTP on :8080 (POST /mint)")
		log.Fatal(http.ListenAndServe(":8080", nil))
	}()
	log.Fatal(tcpServe(":4430"))
	return nil
}
