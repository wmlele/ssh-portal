package validate

import (
	"fmt"
	"net"
	"strconv"
)

// ValidateHost validates a host (IP address or hostname)
func ValidateHost(host string) error {
	if host == "" {
		return fmt.Errorf("host cannot be empty")
	}

	// Handle IPv6 addresses in brackets (e.g., [::1])
	if len(host) > 0 && host[0] == '[' && host[len(host)-1] == ']' {
		ipv6Host := host[1 : len(host)-1]
		if ip := net.ParseIP(ipv6Host); ip == nil {
			return fmt.Errorf("invalid IPv6 address: %s", ipv6Host)
		}
		return nil
	}

	// Check if it's a valid IP address
	if ip := net.ParseIP(host); ip != nil {
		return nil
	}

	// Check for localhost
	if host == "localhost" {
		return nil
	}

	// Validate as hostname
	if len(host) > 253 {
		return fmt.Errorf("hostname too long (max 253 characters)")
	}

	// Basic hostname validation (alphanumeric, dots, hyphens only)
	for _, r := range host {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-') {
			return fmt.Errorf("invalid character in hostname")
		}
	}

	return nil
}

// ValidatePort validates a port number from a string
func ValidatePort(portStr string) (uint16, error) {
	if portStr == "" {
		return 0, fmt.Errorf("port cannot be empty")
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("invalid port: %w", err)
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("port must be between 1 and 65535, got %d", port)
	}
	return uint16(port), nil
}

// ValidateAddress validates a host:port address
func ValidateAddress(addr string, fieldName string) error {
	if addr == "" {
		return fmt.Errorf("%s address cannot be empty", fieldName)
	}

	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid %s address format: %w", fieldName, err)
	}

	if host == "" {
		return fmt.Errorf("%s address must include host", fieldName)
	}

	// Validate host
	if err := ValidateHost(host); err != nil {
		return fmt.Errorf("invalid %s host: %w", fieldName, err)
	}

	// Validate port
	if _, err := ValidatePort(portStr); err != nil {
		return fmt.Errorf("invalid %s port: %w", fieldName, err)
	}

	return nil
}

