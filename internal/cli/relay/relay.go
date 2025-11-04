package relay

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

// ====== TCP rendezvous/splice ======
func tcpServe(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer ln.Close()

	log.Printf("relay TCP listening on %s", addr)

	// Handle accept in a goroutine to allow context cancellation
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			c, err := ln.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					log.Printf("[TCP] accept error: %v", err)
					continue
				}
			}
			log.Printf("[TCP] new connection from %s", c.RemoteAddr())
			go handleTCP(c)
		}
	}()

	// Close listener when context is cancelled
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	// Wait for context cancellation or accept error
	select {
	case <-ctx.Done():
		log.Printf("relay TCP server shutting down...")
		return nil
	case <-acceptDone:
		return nil
	}
}

func handleTCP(c net.Conn) {
	remoteAddr := c.RemoteAddr().String()

	// Parse version + first JSON message (hello or mint)
	msg, br, err := ParseMessage(c)
	if err != nil {
		log.Printf("[TCP] %s -> %v", remoteAddr, err)
		c.Close()
		return
	}

	// Dispatch to appropriate handler
	switch msg.Role {
	case "receiver":
		if msg.Msg == "mint" {
			// Mint invite and attach this connection as the receiver
			ttl := 10 * time.Minute
			if msg.TTLSeconds > 0 && msg.TTLSeconds <= 3600 {
				ttl = time.Duration(msg.TTLSeconds) * time.Second
			}
			inv := MintInvite(msg.ReceiverFP, ttl)
			log.Printf("[MINT] receiver connected: fp=%s code=%s rid=%s expires=%s", msg.ReceiverFP, inv.Code, inv.RID, inv.ExpiresAt.Format(time.RFC3339))
			// Reply with mint_ok
			_ = sendJSON(c, MintOKResponse{Msg: "mint_ok", Code: inv.Code, RID: inv.RID, Exp: inv.ExpiresAt.Unix()})
			// Attach this connection as receiver
			LockInvites()
			inv.ReceiverConn = newBufferedConn(c, br)
			UnlockInvites()
			// Now wait for sender as in receiver attachment
			return
		}
		handleReceiverConnection(c, msg.RID, br)
    case "sender":
        handleSenderConnection(c, msg, br)
	default:
		log.Printf("[TCP] %s -> ERR: unknown role '%s'", remoteAddr, msg.Role)
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
func handleSenderConnection(c net.Conn, msg *EndpointMessage, br *bufio.Reader) {
    inv := HandleSender(c, msg.Code, msg.Sender)
	if inv == nil {
		// Error already handled and connection closed by HandleSender
		return
	}

	// Pair sender with receiver
	// Note: rc is already a bufferedConn that preserves any SSH banner data
	rc := inv.ReceiverConn
	rcAddr := rc.RemoteAddr().String()
	senderAddr := c.RemoteAddr().String()

	log.Printf("[PAIR] successfully paired: sender=%s receiver=%s code=%s rid=%s", senderAddr, rcAddr, inv.Code, inv.RID)

	// Send "ready" message to receiver with sender address
	alg := "" // TODO: extract from receiver connection if available
    readyMsg := ReadyMessage{
		Msg:         "ready",
		SenderAddr:  senderAddr,
		Fingerprint: inv.ReceiverFP,
		Exp:         inv.ExpiresAt.Unix(),
		Alg:         alg,
        Sender:      inv.Sender,
	}
	if err := sendJSON(rc, readyMsg); err != nil {
		log.Printf("[PAIR] failed to send ready to receiver: %v", err)
		rc.Close()
		c.Close()
		return
	}

	// Remove from maps to make it one-shot
	DeleteInvite(inv, "paired")

	// Create splice record
	spliceID := fmt.Sprintf("%d", time.Now().UnixNano())
	splice := &Splice{
		ID:           spliceID,
		Code:         inv.Code,
		RID:          inv.RID,
		ReceiverFP:   inv.ReceiverFP,
		SenderAddr:   senderAddr,
		ReceiverAddr: rcAddr,
		CreatedAt:    time.Now(),
	}

	// Register splice
	spliceMu.Lock()
	splices[spliceID] = splice
	spliceMu.Unlock()

	// Call callback for new splice
	if callbacks != nil && callbacks.OnNewSplice != nil {
		callbacks.OnNewSplice(splice)
	}

	log.Printf("[SPLICE] bridging sender=%s <-> receiver=%s", senderAddr, rcAddr)
	spliceConnections(rc, c, splice) // closes both connections; rc is bufferedConn preserving SSH banner
	log.Printf("[SPLICE] connection closed: sender=%s receiver=%s", senderAddr, rcAddr)
}

func spliceConnections(receiver, sender net.Conn, splice *Splice) {
	defer receiver.Close()
	defer sender.Close()

	done := make(chan struct{}, 2)
	var receiverBytes, senderBytes int64
	receiverAddr := receiver.RemoteAddr().String()
	senderAddr := sender.RemoteAddr().String()

	// receiver -> sender (upstream)
	go func() {
		n, _ := io.Copy(sender, receiver)
		receiverBytes = n
		done <- struct{}{}
	}()
	// sender -> receiver (downstream)
	go func() {
		n, _ := io.Copy(receiver, sender)
		senderBytes = n
		done <- struct{}{}
	}()

	<-done // wait for first direction
	<-done // wait for second direction

	// Update splice stats and mark as closed
	now := time.Now()
	spliceMu.Lock()
	splice.BytesUp = receiverBytes // bytes from receiver to sender
	splice.BytesDown = senderBytes // bytes from sender to receiver
	splice.ClosedAt = &now
	spliceMu.Unlock()

	log.Printf("[SPLICE] stats: %s <-> %s (%d bytes receiver->sender, %d bytes sender->receiver)",
		receiverAddr, senderAddr, receiverBytes, senderBytes)

	// Call callback for closed splice
	if callbacks != nil && callbacks.OnClosedSplice != nil {
		callbacks.OnClosedSplice(splice)
	}
}

// Run executes the relay command
// port is the TCP port number; HTTP will be served on port+1
func Run(port int, interactive bool) error {
	tcpAddr := fmt.Sprintf(":%d", port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartInviteCleanupLoop()

	var wg sync.WaitGroup

	// Start TCP server
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := tcpServe(ctx, tcpAddr); err != nil {
			log.Printf("TCP server error: %v", err)
			cancel() // Signal shutdown on error
		}
	}()

	if interactive {
		// Start TUI for interactive mode
		if err := startTUI(ctx, cancel); err != nil {
			return fmt.Errorf("failed to start TUI: %w", err)
		}
	}

	// Wait for shutdown signal or error
	<-ctx.Done()

	// No HTTP server to shut down (HTTP mint removed)

	// Wait for TCP server to finish
	wg.Wait()
	log.Printf("relay server stopped")
	return nil
}
