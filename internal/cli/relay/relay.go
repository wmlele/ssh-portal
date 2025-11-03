package relay

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"time"
)

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
	DeleteInvite(inv, "paired")

	log.Printf("[PAIR] successfully paired: sender=%s receiver=%s code=%s rid=%s", senderAddr, rcAddr, inv.Code, inv.RID)
	
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
	splice.BytesUp = receiverBytes   // bytes from receiver to sender
	splice.BytesDown = senderBytes   // bytes from sender to receiver
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
	httpAddr := fmt.Sprintf(":%d", port+1)

	StartInviteCleanupLoop()
	StartHTTPServer(httpAddr)
	log.Fatal(tcpServe(tcpAddr))
	return nil
}
