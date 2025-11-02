package receiver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
)

// --- Protocol structures ---

// MintRequest represents a request to mint a new invite
type MintRequest struct {
	ReceiverFP string `json:"receiver_fp"`
}

// MintResponse represents the response from the mint endpoint
type MintResponse struct {
	Code string `json:"code"`
	RID  string `json:"rid"`
	Exp  string `json:"exp"`
}

// ConnectionResult holds the result of connecting to the relay
type ConnectionResult struct {
	Conn net.Conn
	RID  string
	Code string
}

// --- Protocol communication ---

// MintInvite requests a new invite from the relay using the receiver's fingerprint
// relayHost is the relay server host
// relayPort is the TCP port; HTTP will be on port+1
func MintInvite(relayHost string, relayPort int, receiverFP string) (*MintResponse, error) {
	httpPort := relayPort + 1
	relayHTTP := fmt.Sprintf("http://%s/mint", net.JoinHostPort(relayHost, strconv.Itoa(httpPort)))

	body, err := json.Marshal(MintRequest{ReceiverFP: receiverFP})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal mint request: %w", err)
	}

	resp, err := http.Post(relayHTTP, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to POST to relay: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("relay returned status %d", resp.StatusCode)
	}

	var m MintResponse
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, fmt.Errorf("failed to decode mint response: %w", err)
	}

	return &m, nil
}

// ConnectAndHello connects to the relay and sends the HELLO message for a receiver
func ConnectAndHello(relayAddr, rid string) (*ConnectionResult, error) {
	conn, err := net.Dial("tcp", relayAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to relay: %w", err)
	}

	if _, err := fmt.Fprintf(conn, "HELLO receiver rid=%s\n", rid); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to send HELLO: %w", err)
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
	// 1) Mint invite
	mintResp, err := MintInvite(relayHost, relayPort, receiverFP)
	if err != nil {
		return nil, nil, fmt.Errorf("mint failed: %w", err)
	}

	// 2) Connect and send HELLO (TCP uses the specified port)
	relayTCP := net.JoinHostPort(relayHost, strconv.Itoa(relayPort))
	connResult, err := ConnectAndHello(relayTCP, mintResp.RID)
	if err != nil {
		return nil, nil, fmt.Errorf("connection failed: %w", err)
	}

	connResult.Code = mintResp.Code

	return connResult, mintResp, nil
}
