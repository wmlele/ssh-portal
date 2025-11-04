package sender

import (
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"time"
)

// ReverseForward represents a remote (ssh -R) port forward requested by the sender.
// The receiver listens on BindAddr:BindPort and connections are forwarded back to the sender,
// which then connects to LocalTarget and bridges traffic.
type ReverseForward struct {
	ID          string
	BindAddr    string
	BindPort    uint32 // actual bound port on receiver (may be allocated if 0 requested)
	LocalTarget string // local target address on sender side (e.g., 127.0.0.1:22)
	Listener    net.Listener
	CreatedAt   time.Time
}

var (
	reverseFwdsMu sync.RWMutex
	reverseFwds   = make(map[string]*ReverseForward)
)

// StartReverseForward requests a remote port forward on the receiver (ssh -R style).
// bindAddr is the address on receiver to bind (e.g., 0.0.0.0 or 127.0.0.1), bindPort can be 0 to auto-assign.
// localTarget is the address on the sender machine to forward connections to (e.g., 127.0.0.1:22).
// Returns the forward ID and the actual bound port.
func StartReverseForward(bindAddr string, bindPort uint32, localTarget string) (string, uint32, error) {
	sshClientMu.RLock()
	client := sshClient
	sshClientMu.RUnlock()
	if client == nil {
		return "", 0, fmt.Errorf("SSH client not connected")
	}

	remote := net.JoinHostPort(bindAddr, strconv.FormatUint(uint64(bindPort), 10))
	ln, err := client.Listen("tcp", remote)
	if err != nil {
		return "", 0, fmt.Errorf("remote listen failed on %s: %w", remote, err)
	}

	// Determine the actual bound port (useful if 0 requested)
	actualPort := uint32(0)
	if addr, ok := ln.Addr().(*net.TCPAddr); ok {
		actualPort = uint32(addr.Port)
	}

	id := fmt.Sprintf("%d", time.Now().UnixNano())
	rf := &ReverseForward{
		ID:          id,
		BindAddr:    bindAddr,
		BindPort:    actualPort,
		LocalTarget: localTarget,
		Listener:    ln,
		CreatedAt:   time.Now(),
	}

	reverseFwdsMu.Lock()
	reverseFwds[id] = rf
	reverseFwdsMu.Unlock()

	// Accept loop: for each remote connection, connect to local target and bridge
	go func(id string, l net.Listener, target string) {
		for {
			rc, err := l.Accept()
			if err != nil {
				// Listener closed or error
				log.Printf("[SENDER R-FWD] listener closed id=%s: %v", id, err)
				return
			}
			go func(remoteConn net.Conn) {
				defer remoteConn.Close()
				lc, err := net.Dial("tcp", target)
				if err != nil {
					log.Printf("[SENDER R-FWD] dial local target %s failed: %v", target, err)
					return
				}
				defer lc.Close()
				// Bridge both directions
				go io.Copy(lc, remoteConn)
				_, _ = io.Copy(remoteConn, lc)
			}(rc)
		}
	}(id, ln, localTarget)

	return id, actualPort, nil
}

// StopReverseForward cancels a previously started remote forward by ID.
func StopReverseForward(id string) error {
	reverseFwdsMu.Lock()
	rf, ok := reverseFwds[id]
	if ok {
		delete(reverseFwds, id)
	}
	reverseFwdsMu.Unlock()
	if !ok {
		return fmt.Errorf("reverse forward %s not found", id)
	}
	if rf.Listener != nil {
		if err := rf.Listener.Close(); err != nil {
			return fmt.Errorf("close listener: %w", err)
		}
	}
	return nil
}

// GetAllReverseForwards returns all active reverse forwards.
func GetAllReverseForwards() []*ReverseForward {
	reverseFwdsMu.RLock()
	defer reverseFwdsMu.RUnlock()
	result := make([]*ReverseForward, 0, len(reverseFwds))
	for _, rf := range reverseFwds {
		result = append(result, rf)
	}
	return result
}
