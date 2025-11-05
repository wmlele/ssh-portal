package receiver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
)

// --- Protocol structures ---

// AwaitMessage is the JSON await message sent to the relay before SSH starts
type AwaitMessage struct {
	Msg  string `json:"msg"`
	Role string `json:"role"`
	Code string `json:"code,omitempty"`
	RID  string `json:"rid,omitempty"`
}

// JSON hello message/response over TCP
type HelloRequest struct {
	Msg        string `json:"msg"` // "hello"
	Role       string `json:"role"`
	ReceiverFP string `json:"receiver_fp"`
}

type HelloResponse struct {
	Msg  string `json:"msg"` // "hello_ok"
	Code string `json:"code"`
	RID  string `json:"rid"`
	Exp  int64  `json:"exp"`
}

// ReadyMessage is received from relay when sender connects
type ReadyMessage struct {
	Msg         string      `json:"msg"` // "ready"
	SenderAddr  string      `json:"sender_addr"`
	Fingerprint string      `json:"fp"`
	Exp         int64       `json:"exp"`
	Alg         string      `json:"alg,omitempty"`
	Sender      *SenderInfo `json:"sender,omitempty"`
}

// SenderInfo mirrors metadata provided by sender via relay
type SenderInfo struct {
	Keepalive int    `json:"keepalive,omitempty"`
	Identity  string `json:"identity,omitempty"`
}

// ConnectionResult holds the result of connecting to the relay
type ConnectionResult struct {
	Conn         net.Conn
	RID          string
	Code         string
	ReadyMessage *ReadyMessage // populated after receiving "ready" from relay
}

// --- Protocol communication ---

// ConnectToRelay performs the full protocol handshake for a receiver:
// 1. Mints an invite using the receiver's fingerprint
// 2. Connects to relay and sends AWAIT message
// Returns the connection and invite information
// relayHost is the relay server host
// relayPort is the TCP port (HTTP will be on port+1)
func ConnectToRelay(relayHost string, relayPort int, receiverFP string) (*ConnectionResult, *HelloResponse, error) {
	// 1) Connect TCP
	relayTCP := net.JoinHostPort(relayHost, strconv.Itoa(relayPort))
	conn, err := net.Dial("tcp", relayTCP)
	if err != nil {
		return nil, nil, fmt.Errorf("socket error: %w", err)
	}
	// 2) Send version + JSON hello
	if _, err := fmt.Fprintln(conn, "ssh-relay/1.0"); err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("failed to send version: %w", err)
	}
	if err := json.NewEncoder(conn).Encode(HelloRequest{Msg: "hello", Role: "receiver", ReceiverFP: receiverFP}); err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("failed to send hello: %w", err)
	}
	// 3) Read hello_ok response
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("failed to read hello response: %w", err)
	}
	var m HelloResponse
	if err := json.Unmarshal([]byte(line), &m); err != nil || m.Msg != "hello_ok" {
		conn.Close()
		return nil, nil, fmt.Errorf("bad hello response")
	}

	// 4) On same connection, send await with RID to attach
	awaitMsg := AwaitMessage{Msg: "await", Role: "receiver", RID: m.RID}
	log.Printf("Sent await to relay: role=receiver rid=%s", m.RID)
	if err := json.NewEncoder(conn).Encode(awaitMsg); err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("failed to send await: %w", err)
	}

	// Return connection result without ready message - receiver will wait for it separately
	return &ConnectionResult{
		Conn:         conn,
		RID:          m.RID,
		Code:         m.Code,
		ReadyMessage: nil, // Will be set after waiting for ready
	}, &m, nil
}

// bufferedConn wraps a net.Conn with a bufio.Reader to preserve buffered data
type bufferedConn struct {
	net.Conn
	br *bufio.Reader
}

func (bc *bufferedConn) Read(p []byte) (int, error) {
	return bc.br.Read(p)
}

// WaitForReady waits for and reads the "ready" message from the relay connection
// Returns the ready message and a buffered reader that preserves any SSH data
func WaitForReady(conn net.Conn) (*ReadyMessage, *bufio.Reader, error) {
	br := bufio.NewReader(conn)
	readyLine, err := br.ReadString('\n')
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read ready message: %w", err)
	}
	var ready ReadyMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(readyLine)), &ready); err != nil || ready.Msg != "ready" {
		return nil, nil, fmt.Errorf("bad ready message: %w", err)
	}
	return &ready, br, nil
}
