package usercode

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	bip39 "github.com/tyler-smith/go-bip39/wordlists"
)

// =========================
// Public API
// =========================

// GenerateRelayCode returns a base64 (raw, no padding) string representing 32 bits of entropy (4 bytes).
func GenerateRelayCode() (string, error) {
	return gen32b()
}

// GenerateReceiverCode returns a base64 (raw, no padding) string representing 32 bits of entropy (4 bytes).
func GenerateReceiverCode() (string, error) {
	return gen32b()
}

// generateUserCode returns the userCode (and fullCode base64) from two base64 32-bit codes.
func GenerateUserCode(relayCodeB64, receiverCodeB64 string) (userCode, fullCodeB64 string, err error) {
	rb, err := decode32b(relayCodeB64)
	if err != nil {
		return "", "", fmt.Errorf("relayCode: %w", err)
	}
	sb, err := decode32b(receiverCodeB64)
	if err != nil {
		return "", "", fmt.Errorf("receiverCode: %w", err)
	}
	full := append(rb, sb...)
	user, err := fullToUserCode(full)
	if err != nil {
		return "", "", err
	}
	return user, encode64b(full), nil
}

// parseUserCode parses a userCode and returns relayCode and receiverCode (both base64, raw no padding).
func ParseUserCode(userCode string) (relayCodeB64, receiverCodeB64 string, fullCodeB64 string, err error) {
	full, err := userCodeToFull(userCode)
	if err != nil {
		return "", "", "", err
	}
	if len(full) != 8 {
		return "", "", "", errors.New("decoded fullCode has invalid length")
	}
	return encode32b(full[:4]), encode32b(full[4:]), encode64b(full), nil
}

// =========================
// Internals
// =========================

var (
	// BIP39 English list (2048 words) via go-bip39 wordlists package.
	words = bip39.English
	// quick reverse lookup
	wordIndex = func() map[string]uint16 {
		m := make(map[string]uint16, len(words))
		for i, w := range words {
			m[strings.ToLower(w)] = uint16(i)
		}
		return m
	}()
)

func gen32b() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return encode32b(b), nil
}

func encode32b(b []byte) string {
	// raw base64, no '=' padding
	return base64.RawStdEncoding.EncodeToString(b)
}

func decode32b(s string) ([]byte, error) {
	// accept raw or padded
	dec, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil {
		// fallback to padded
		dec2, err2 := base64.StdEncoding.DecodeString(s)
		if err2 != nil {
			return nil, err
		}
		dec = dec2
	}
	if len(dec) != 4 {
		return nil, fmt.Errorf("expected 4 bytes, got %d", len(dec))
	}
	return dec, nil
}

func encode64b(b []byte) string {
	return base64.RawStdEncoding.EncodeToString(b)
}

func fullToUserCode(full []byte) (string, error) {
	if len(full) != 8 {
		return "", errors.New("fullCode must be 8 bytes (64 bits)")
	}
	// Interpret as big-endian 64-bit integer for stable mapping
	var u uint64
	for i := 0; i < 8; i++ {
		u = (u << 8) | uint64(full[i])
	}

	// Top 44 bits -> 4 words (each 11 bits)
	w1 := (u >> 53) & 0x7FF // bits 63..53
	w2 := (u >> 42) & 0x7FF // bits 52..42
	w3 := (u >> 31) & 0x7FF // bits 41..31
	w4 := (u >> 20) & 0x7FF // bits 30..20
	// Low 20 bits -> numeric (0..1,048,575) -> 7 decimal digits, zero-padded
	num := int(u & 0xFFFFF)
	if int(w1) >= len(words) || int(w2) >= len(words) || int(w3) >= len(words) || int(w4) >= len(words) {
		return "", errors.New("word index out of range (should not happen)")
	}
	numStr := fmt.Sprintf("%07d", num)     // 7 digits
	numStr = numStr[:3] + "-" + numStr[3:] // format xxx-xxxx

	return fmt.Sprintf("%s-%s-%s-%s-%s",
		words[w1], words[w2], words[w3], words[w4], numStr), nil
}

func userCodeToFull(code string) ([]byte, error) {
	parts := strings.Split(code, "-")
	if len(parts) != 6 {
		return nil, errors.New("userCode must look like word-word-word-word-123-4567")
	}
	wStrs := parts[:4]
	d1 := parts[4]
	d2 := parts[5]

	// Map words (case-insensitive) to 11-bit indices
	var idx [4]uint64
	for i := 0; i < 4; i++ {
		w := strings.ToLower(strings.TrimSpace(wStrs[i]))
		id, ok := wordIndex[w]
		if !ok {
			return nil, fmt.Errorf("word %q not in BIP39 English list", wStrs[i])
		}
		idx[i] = uint64(id)
	}

	// Parse 3+4 digits
	if len(d1) != 3 || len(d2) != 4 {
		return nil, errors.New("numeric part must be 3 digits then 4 digits")
	}
	if !allDigits(d1) || !allDigits(d2) {
		return nil, errors.New("numeric part must contain only digits")
	}
	numVal := atoiUnsafe(d1)*10000 + atoiUnsafe(d2) // 0..9,999,999
	if numVal > 0xFFFFF {                           // > 1,048,575 is invalid for our 20-bit mapping
		return nil, errors.New("numeric part out of range for 20-bit payload")
	}

	// Recompose 64-bit big-endian
	var u uint64
	u |= (idx[0] & 0x7FF) << 53
	u |= (idx[1] & 0x7FF) << 42
	u |= (idx[2] & 0x7FF) << 31
	u |= (idx[3] & 0x7FF) << 20
	u |= uint64(numVal) // low 20 bits

	// to bytes
	full := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		full[i] = byte(u & 0xFF)
		u >>= 8
	}
	return full, nil
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// atoiUnsafe: fast decimal parse for fixed-length numeric pieces (we validated with allDigits)
func atoiUnsafe(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n
}
