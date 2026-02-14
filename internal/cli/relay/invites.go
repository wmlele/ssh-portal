package relay

import (
	"crypto/rand"
	"encoding/base32"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"ssh-portal/internal/cli/usercode"
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
	Sender       *SenderInfo
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

// Rate limiting for code guessing
const (
	rateLimitThreshold = 3              // free attempts before throttling
	rateLimitDelay     = 3 * time.Second // delay per attempt once throttled
	rateLimitWindow    = time.Minute     // failures expire after this
)

type rateLimitEntry struct {
	count    int
	lastFail time.Time
}

var (
	invMu   sync.RWMutex
	invByID = map[string]*Invite{}
	invByCd = map[string]*Invite{}

	spliceMu sync.RWMutex
	splices  = map[string]*Splice{}

	rateMu         sync.Mutex
	failedAttempts = map[string]*rateLimitEntry{}

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
	rid := randB32(16)                         // rendezvous id (base32)
	code, _ := usercode.GenerateReceiverCode() // receiver code; discard error or second value for now
	exp := time.Now().Add(ttl).UTC()           // expiry
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
		cleanupRateLimitEntries()
	}
}

// checkRateLimit sleeps if the given IP has exceeded the failure threshold.
func checkRateLimit(ip string) {
	rateMu.Lock()
	entry := failedAttempts[ip]
	rateMu.Unlock()

	if entry == nil {
		return
	}
	if time.Since(entry.lastFail) > rateLimitWindow {
		return
	}
	if entry.count >= rateLimitThreshold {
		log.Printf("[RATE] throttling %s (%d failures)", ip, entry.count)
		time.Sleep(rateLimitDelay)
	}
}

// recordFailedAttempt increments the failure counter for an IP.
func recordFailedAttempt(ip string) {
	rateMu.Lock()
	defer rateMu.Unlock()

	entry := failedAttempts[ip]
	if entry == nil {
		entry = &rateLimitEntry{}
		failedAttempts[ip] = entry
	}
	// Reset if the window has elapsed since the last failure.
	if time.Since(entry.lastFail) > rateLimitWindow {
		entry.count = 0
	}
	entry.count++
	entry.lastFail = time.Now()
}

// clearFailedAttempts removes the failure record for an IP.
func clearFailedAttempts(ip string) {
	rateMu.Lock()
	delete(failedAttempts, ip)
	rateMu.Unlock()
}

// cleanupRateLimitEntries removes stale rate-limit entries.
func cleanupRateLimitEntries() {
	rateMu.Lock()
	defer rateMu.Unlock()
	now := time.Now()
	for ip, entry := range failedAttempts {
		if now.Sub(entry.lastFail) > rateLimitWindow {
			delete(failedAttempts, ip)
		}
	}
}

func randB32(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return strings.TrimRight(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), "=")
}
