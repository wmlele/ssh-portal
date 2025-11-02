package relay

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Invite represents a connection invitation
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

// GetByRID retrieves an invite by rendezvous ID
func GetByRID(rid string) *Invite {
	invMu.Lock()
	defer invMu.Unlock()
	return invByID[rid]
}

// GetByCode retrieves an invite by code
func GetByCode(code string) *Invite {
	invMu.Lock()
	defer invMu.Unlock()
	return invByCd[code]
}

// MintInvite creates a new invite for the given receiver fingerprint
func MintInvite(receiverFP string, ttl time.Duration) *Invite {
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

// DeleteInvite removes an invite from both maps
func DeleteInvite(inv *Invite) {
	invMu.Lock()
	delete(invByID, inv.RID)
	delete(invByCd, inv.Code)
	invMu.Unlock()
}

// LockInvites locks the invite mutex (for external access)
func LockInvites() {
	invMu.Lock()
}

// UnlockInvites unlocks the invite mutex
func UnlockInvites() {
	invMu.Unlock()
}

// StartInviteCleanupLoop starts the background cleanup goroutine
func StartInviteCleanupLoop() {
	go cleanupLoop()
}

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
	inv := MintInvite(req.ReceiverFP, ttl)
	log.Printf("[MINT] receiver connected: fp=%s code=%s rid=%s expires=%s", req.ReceiverFP, inv.Code, inv.RID, inv.ExpiresAt.Format(time.RFC3339))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(mintResp{Code: inv.Code, RID: inv.RID, Exp: inv.ExpiresAt})
}

// StartHTTPServer starts the HTTP server for mint endpoint
func StartHTTPServer(addr string) {
	http.HandleFunc("/mint", handleMint)
	log.Printf("relay HTTP on %s (POST /mint)", addr)
	go func() {
		log.Fatal(http.ListenAndServe(addr, nil))
	}()
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

