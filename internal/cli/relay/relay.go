package relay

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
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
	DeleteInvite(inv)

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

// Run executes the relay command
// port is the TCP port number; HTTP will be served on port+1
func Run(port int) error {
	tcpAddr := fmt.Sprintf(":%d", port)
	httpAddr := fmt.Sprintf(":%d", port+1)

	StartInviteCleanupLoop()
	StartHTTPServer(httpAddr)
	log.Fatal(tcpServe(tcpAddr))
	return nil
}
