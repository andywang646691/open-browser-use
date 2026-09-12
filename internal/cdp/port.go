package cdp

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type PortInfo struct {
	Port uint16
	Path string
}

func (p PortInfo) WebSocketURL() string {
	return fmt.Sprintf("ws://127.0.0.1:%d%s", p.Port, p.Path)
}

// ParseDevToolsActivePort parses and validates the content of a DevToolsActivePort file.
// Format:
// Line 1: port number (1-65535)
// Line 2: WebSocket path (must start with /devtools/browser/ or /devtools/)
func ParseDevToolsActivePort(content string) (PortInfo, error) {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) < 2 {
		return PortInfo{}, errors.New("DevToolsActivePort must contain at least port and path lines")
	}

	portStr := strings.TrimSpace(lines[0])
	portNum, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil || portNum == 0 {
		return PortInfo{}, fmt.Errorf("invalid DevToolsActivePort port %q: must be 1-65535", portStr)
	}

	wsPath := strings.TrimSpace(lines[1])
	if !strings.HasPrefix(wsPath, "/devtools/") {
		return PortInfo{}, fmt.Errorf("invalid DevToolsActivePort path %q: must start with /devtools/", wsPath)
	}

	// Security: validate that path has no line breaks, control characters, or path traversal
	if strings.ContainsAny(wsPath, "\r\n\t") || strings.Contains(wsPath, "..") {
		return PortInfo{}, fmt.Errorf("invalid DevToolsActivePort path %q: contains illegal characters", wsPath)
	}

	// Ensure path parses as valid URL path
	parsed, err := url.Parse(wsPath)
	if err != nil || parsed.Path != wsPath {
		return PortInfo{}, fmt.Errorf("invalid DevToolsActivePort path %q: must be a clean URL path", wsPath)
	}

	return PortInfo{
		Port: uint16(portNum),
		Path: wsPath,
	}, nil
}

// ReadDevToolsActivePort reads and validates DevToolsActivePort from the specified user data dir.
func ReadDevToolsActivePort(userDataDir string) (PortInfo, error) {
	if strings.TrimSpace(userDataDir) == "" {
		return PortInfo{}, errors.New("user data directory is empty")
	}
	filePath := filepath.Join(userDataDir, "DevToolsActivePort")
	data, err := os.ReadFile(filePath)
	if err != nil {
		return PortInfo{}, fmt.Errorf("read DevToolsActivePort at %s: %w", filePath, err)
	}
	return ParseDevToolsActivePort(string(data))
}

// ValidateEndpointSecurity ensures an endpoint is strictly loopback (127.0.0.1 or ::1).
func ValidateEndpointSecurity(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid endpoint URL: %w", err)
	}
	if u.Scheme != "ws" && u.Scheme != "http" {
		return fmt.Errorf("unsupported endpoint scheme %q: only ws/http allowed", u.Scheme)
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if ip != nil {
		if !ip.IsLoopback() {
			return fmt.Errorf("security violation: endpoint IP %s is not loopback", host)
		}
		return nil
	}
	if host != "localhost" {
		return fmt.Errorf("security violation: endpoint host %s is not loopback/localhost", host)
	}
	return nil
}
