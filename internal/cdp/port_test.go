package cdp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseDevToolsActivePort(t *testing.T) {
	t.Run("valid content", func(t *testing.T) {
		content := "9222\n/devtools/browser/fd39c8a7-c6b6-4a67-a0e3-fef27c3d9310\n"
		info, err := ParseDevToolsActivePort(content)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Port != 9222 {
			t.Errorf("expected port 9222, got %d", info.Port)
		}
		if info.Path != "/devtools/browser/fd39c8a7-c6b6-4a67-a0e3-fef27c3d9310" {
			t.Errorf("unexpected path %q", info.Path)
		}
		expectedWS := "ws://127.0.0.1:9222/devtools/browser/fd39c8a7-c6b6-4a67-a0e3-fef27c3d9310"
		if info.WebSocketURL() != expectedWS {
			t.Errorf("expected %q, got %q", expectedWS, info.WebSocketURL())
		}
	})

	t.Run("too few lines", func(t *testing.T) {
		_, err := ParseDevToolsActivePort("9222\n")
		if err == nil {
			t.Fatal("expected error for single line")
		}
	})

	t.Run("invalid port", func(t *testing.T) {
		_, err := ParseDevToolsActivePort("0\n/devtools/browser/xyz")
		if err == nil {
			t.Fatal("expected error for port 0")
		}
		_, err = ParseDevToolsActivePort("999999\n/devtools/browser/xyz")
		if err == nil {
			t.Fatal("expected error for port > 65535")
		}
		_, err = ParseDevToolsActivePort("abc\n/devtools/browser/xyz")
		if err == nil {
			t.Fatal("expected error for non-numeric port")
		}
	})

	t.Run("invalid path", func(t *testing.T) {
		_, err := ParseDevToolsActivePort("9222\n/other/path")
		if err == nil {
			t.Fatal("expected error for path not starting with /devtools/")
		}
		_, err = ParseDevToolsActivePort("9222\n/devtools/../traversal")
		if err == nil {
			t.Fatal("expected error for path traversal")
		}
		_, err = ParseDevToolsActivePort("9222\n/devtools/bad\rpath")
		if err == nil {
			t.Fatal("expected error for CRLF in path")
		}
	})
}

func TestReadDevToolsActivePort(t *testing.T) {
	tempDir := t.TempDir()
	portFile := filepath.Join(tempDir, "DevToolsActivePort")
	if err := os.WriteFile(portFile, []byte("9333\n/devtools/browser/test-guid\n"), 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	info, err := ReadDevToolsActivePort(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Port != 9333 {
		t.Errorf("expected port 9333, got %d", info.Port)
	}
	if info.Path != "/devtools/browser/test-guid" {
		t.Errorf("unexpected path %q", info.Path)
	}

	_, err = ReadDevToolsActivePort(filepath.Join(tempDir, "nonexistent"))
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestValidateEndpointSecurity(t *testing.T) {
	t.Run("valid loopback addresses", func(t *testing.T) {
		validURLs := []string{
			"ws://127.0.0.1:9222/devtools/browser/xyz",
			"ws://localhost:9222/devtools/browser/xyz",
			"ws://[::1]:9222/devtools/browser/xyz",
		}
		for _, u := range validURLs {
			if err := ValidateEndpointSecurity(u); err != nil {
				t.Errorf("expected %q to be valid, got %v", u, err)
			}
		}
	})

	t.Run("reject non-loopback addresses", func(t *testing.T) {
		invalidURLs := []string{
			"ws://192.168.1.1:9222/devtools/browser/xyz",
			"ws://10.0.0.1:9222/devtools/browser/xyz",
			"ws://google.com:9222/devtools/browser/xyz",
			"ws://example.com/devtools/browser/xyz",
			"ftp://127.0.0.1:9222/devtools/browser/xyz",
		}
		for _, u := range invalidURLs {
			if err := ValidateEndpointSecurity(u); err == nil {
				t.Errorf("expected %q to be rejected", u)
			}
		}
	})
}
