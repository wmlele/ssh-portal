package relay

import (
	"context"
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
	CreatedAt    time.Time
}

// Splice represents an established connection between sender and receiver
type Splice struct {
	ID           string
	Code         string
	RID          string
	ReceiverFP   string
	SenderAddr   string
	ReceiverAddr string
	CreatedAt    time.Time
	BytesUp      int64 // bytes from receiver to sender
	BytesDown    int64 // bytes from sender to receiver
	ClosedAt     *time.Time
}

// Event callbacks
type EventCallbacks struct {
	OnNewInvite    func(*Invite)
	OnClosedInvite func(*Invite, string) // invite, reason
	OnNewSplice    func(*Splice)
	OnClosedSplice func(*Splice)
}

var (
	invMu   sync.RWMutex
	invByID = map[string]*Invite{}
	invByCd = map[string]*Invite{}

	spliceMu sync.RWMutex
	splices  = map[string]*Splice{}

	callbacks *EventCallbacks
)

// SetEventCallbacks sets the event callbacks for relay events
func SetEventCallbacks(cb *EventCallbacks) {
	callbacks = cb
}

// GetOutstandingInvites returns all outstanding (not expired) invites
func GetOutstandingInvites() []*Invite {
	invMu.RLock()
	defer invMu.RUnlock()

	now := time.Now()
	result := make([]*Invite, 0, len(invByID))
	for _, inv := range invByID {
		if now.Before(inv.ExpiresAt) {
			result = append(result, inv)
		}
	}
	return result
}

// GetEstablishedSplices returns all established splices (active and closed)
func GetEstablishedSplices() []*Splice {
	spliceMu.RLock()
	defer spliceMu.RUnlock()

	result := make([]*Splice, 0, len(splices))
	for _, s := range splices {
		result = append(result, s)
	}
	return result
}

// GetActiveSplices returns only active (not closed) splices
func GetActiveSplices() []*Splice {
	spliceMu.RLock()
	defer spliceMu.RUnlock()

	result := make([]*Splice, 0)
	for _, s := range splices {
		if s.ClosedAt == nil {
			result = append(result, s)
		}
	}
	return result
}

// GetByRID retrieves an invite by rendezvous ID
func GetByRID(rid string) *Invite {
	invMu.RLock()
	defer invMu.RUnlock()
	return invByID[rid]
}

// GetByCode retrieves an invite by code
func GetByCode(code string) *Invite {
	invMu.RLock()
	defer invMu.RUnlock()
	return invByCd[code]
}

// MintInvite creates a new invite for the given receiver fingerprint
func MintInvite(receiverFP string, ttl time.Duration) *Invite {
	rid := randB32(16)               // rendezvous id (base32)
	code := fmtCode()                // human code
	exp := time.Now().Add(ttl).UTC() // expiry
	now := time.Now().UTC()
	inv := &Invite{
		RID:        rid,
		Code:       code,
		ReceiverFP: receiverFP,
		ExpiresAt:  exp,
		CreatedAt:  now,
	}
	invMu.Lock()
	invByID[rid] = inv
	invByCd[code] = inv
	invMu.Unlock()

	// Call callback if set
	if callbacks != nil && callbacks.OnNewInvite != nil {
		callbacks.OnNewInvite(inv)
	}

	return inv
}

// DeleteInvite removes an invite from both maps
// reason should be provided for logging/tracking purposes
func DeleteInvite(inv *Invite, reason string) {
	invMu.Lock()
	delete(invByID, inv.RID)
	delete(invByCd, inv.Code)
	invMu.Unlock()

	// Call callback if set
	if callbacks != nil && callbacks.OnClosedInvite != nil {
		callbacks.OnClosedInvite(inv, reason)
	}
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
		var toCleanup []*Invite
		for _, v := range invByID {
			if now.After(v.ExpiresAt) {
				toCleanup = append(toCleanup, v)
			}
		}
		invMu.Unlock()

		cleaned := 0
		for _, v := range toCleanup {
			if v.ReceiverConn != nil {
				log.Printf("[CLEANUP] closing expired connection: code=%s rid=%s", v.Code, v.RID)
				v.ReceiverConn.Close()
			}
			DeleteInvite(v, "expired")
			cleaned++
		}
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
// Returns the http.Server for graceful shutdown
func StartHTTPServer(ctx context.Context, addr string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/mint", handleMint)
	
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	log.Printf("relay HTTP on %s (POST /mint)", addr)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	return server
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
