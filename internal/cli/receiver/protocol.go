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

// JSONHello is the JSON hello message sent to the relay before SSH starts
type HelloMessage struct {
	Msg  string `json:"msg"`
	Role string `json:"role"`
	Code string `json:"code,omitempty"`
	RID  string `json:"rid,omitempty"`
}

// JSON mint message/response over TCP
type MintRequest struct {
	Msg        string `json:"msg"` // "mint"
	Role       string `json:"role"`
	ReceiverFP string `json:"receiver_fp"`
}

type MintResponse struct {
	Msg  string `json:"msg"` // "mint_ok"
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

// OKResponse is the response sent by relay after hello
type OKResponse struct {
	Msg string `json:"msg"` // "ok"
	FP  string `json:"fp,omitempty"`
	Exp int64  `json:"exp,omitempty"`
	Alg string `json:"alg,omitempty"`
}

// ConnectionResult holds the result of connecting to the relay
type ConnectionResult struct {
	Conn         net.Conn
	RID          string
	Code         string
	ReadyMessage *ReadyMessage // populated after receiving "ready" from relay
}

// --- Protocol communication ---

// (HTTP mint endpoint removed; mint is performed over TCP JSON)

// ConnectAndHello connects to the relay and sends the HELLO message for a receiver
func ConnectAndHello(relayAddr, rid string) (*ConnectionResult, error) {
	conn, err := net.Dial("tcp", relayAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to relay: %w", err)
	}

	// Send version + JSON hello
	if _, err := fmt.Fprintln(conn, "ssh-relay/1.0"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to send version: %w", err)
	}
	if err := json.NewEncoder(conn).Encode(HelloMessage{Msg: "hello", Role: "receiver", RID: rid}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to send hello: %w", err)
	}

	return &ConnectionResult{
		Conn: conn,
		RID:  rid,
	}, nil
}

// ConnectToRelay performs the full protocol handshake for a receiver:
// 1. Mints an invite using the receiver's fingerprint
// 2. Connects to relay and sends HELLO message
// Returns the connection and invite information
// relayHost is the relay server host
// relayPort is the TCP port (HTTP will be on port+1)
func ConnectToRelay(relayHost string, relayPort int, receiverFP string) (*ConnectionResult, *MintResponse, error) {
	// 1) Connect TCP
	relayTCP := net.JoinHostPort(relayHost, strconv.Itoa(relayPort))
	conn, err := net.Dial("tcp", relayTCP)
	if err != nil {
		return nil, nil, fmt.Errorf("socket error: %w", err)
	}
	// 2) Send version + JSON mint
	if _, err := fmt.Fprintln(conn, "ssh-relay/1.0"); err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("failed to send version: %w", err)
	}
	if err := json.NewEncoder(conn).Encode(MintRequest{Msg: "mint", Role: "receiver", ReceiverFP: receiverFP}); err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("failed to send mint: %w", err)
	}
	// 3) Read mint_ok response
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("failed to read mint response: %w", err)
	}
	var m MintResponse
	if err := json.Unmarshal([]byte(line), &m); err != nil || m.Msg != "mint_ok" {
		conn.Close()
		return nil, nil, fmt.Errorf("bad mint response")
	}

	// 4) On same connection, send hello with RID to attach
	helloMsg := HelloMessage{Msg: "hello", Role: "receiver", RID: m.RID}
	log.Printf("Sent hello to relay: role=receiver rid=%s", m.RID)
	if err := json.NewEncoder(conn).Encode(helloMsg); err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("failed to send hello: %w", err)
	}

	// Return connection result without ready message - receiver will wait for it separately
	return &ConnectionResult{
		Conn:         conn,
		RID:          m.RID,
		Code:         m.Code,
		ReadyMessage: nil, // Will be set after waiting for ready
	}, &m, nil
}

// WaitForReady waits for and reads the "ready" message from the relay connection
func WaitForReady(conn net.Conn) (*ReadyMessage, error) {
	br := bufio.NewReader(conn)
	readyLine, err := br.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read ready message: %w", err)
	}
	var ready ReadyMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(readyLine)), &ready); err != nil || ready.Msg != "ready" {
		return nil, fmt.Errorf("bad ready message: %w", err)
	}
	return &ready, nil
}
