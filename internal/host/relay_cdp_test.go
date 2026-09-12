package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ifuryst/open-browser-use/internal/cdp"
	"github.com/ifuryst/open-browser-use/internal/wire"
)

type mockRelayServer struct {
	server   *httptest.Server
	wsURL    string
	commands []string
	mu       sync.Mutex
}

func setupRelayMockCDP(t *testing.T) *mockRelayServer {
	m := &mockRelayServer{}
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			_, msgBytes, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg map[string]any
			if err := json.Unmarshal(msgBytes, &msg); err != nil {
				continue
			}
			method, _ := msg["method"].(string)
			id := msg["id"]
			m.mu.Lock()
			m.commands = append(m.commands, method)
			m.mu.Unlock()

			if method == "Target.attachToTarget" {
				_ = conn.WriteJSON(map[string]any{
					"id":     id,
					"result": map[string]any{"sessionId": "cdp-sess-1"},
				})
			} else if method == "Target.detachFromTarget" {
				_ = conn.WriteJSON(map[string]any{"id": id, "result": map[string]any{}})
			} else if method == "Runtime.evaluate" {
				_ = conn.WriteJSON(map[string]any{
					"id":     id,
					"result": map[string]any{"result": map[string]any{"value": "eval-ok"}},
				})
			} else {
				_ = conn.WriteJSON(map[string]any{"id": id, "result": map[string]any{}})
			}
		}
	}))
	m.wsURL = "ws://" + m.server.Listener.Addr().String() + "/devtools/browser/test"
	return m
}

func TestRelayCDPModeExplicitExtension(t *testing.T) {
	hostToExtensionReader, hostToExtensionWriter := net.Pipe()
	defer hostToExtensionReader.Close()
	defer hostToExtensionWriter.Close()
	extensionToHostReader, extensionToHostWriter := net.Pipe()
	defer extensionToHostReader.Close()
	defer extensionToHostWriter.Close()

	socketDir := shortSocketDir(t)
	socketPath := filepath.Join(socketDir, "obu.sock")
	relay := NewRelay(Config{
		SocketDir:  socketDir,
		SocketPath: socketPath,
		CDPMode:    "extension",
	}, extensionToHostReader, hostToExtensionWriter)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = relay.Serve(ctx) }()
	waitForSocket(t, socketPath)

	// Extension background loop responding to extension requests
	go func() {
		for {
			payload, err := wire.ReadFrame(hostToExtensionReader, wire.DefaultMaxFrame)
			if err != nil {
				return
			}
			var req map[string]any
			_ = json.Unmarshal(payload, &req)
			id := req["id"]
			_ = wire.WriteFrame(extensionToHostWriter, []byte(`{"jsonrpc":"2.0","id":"`+id.(string)+`","result":{"from":"extension"}}`))
		}
	}()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial unix: %v", err)
	}
	defer conn.Close()

	// Send attach in extension mode
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      "client-1",
		"method":  "attach",
		"params":  map[string]any{"tabId": 101, "_cdpMode": "extension"},
	}
	payload, _ := json.Marshal(req)
	_ = wire.WriteFrame(conn, payload)

	respBytes, err := wire.ReadFrame(conn, wire.DefaultMaxFrame)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}

	var resp map[string]any
	_ = json.Unmarshal(respBytes, &resp)
	res, _ := resp["result"].(map[string]any)
	if res["from"] != "extension" {
		t.Errorf("expected response from extension, got %v", resp)
	}
}

func TestRelayCDPModeDirectWithManager(t *testing.T) {
	hostToExtensionReader, hostToExtensionWriter := net.Pipe()
	defer hostToExtensionReader.Close()
	defer hostToExtensionWriter.Close()
	extensionToHostReader, extensionToHostWriter := net.Pipe()
	defer extensionToHostReader.Close()
	defer extensionToHostWriter.Close()

	mockCDP := setupRelayMockCDP(t)
	defer mockCDP.server.Close()

	socketDir := shortSocketDir(t)
	socketPath := filepath.Join(socketDir, "obu.sock")
	relay := NewRelay(Config{
		SocketDir:  socketDir,
		SocketPath: socketPath,
		CDPMode:    "direct",
	}, extensionToHostReader, hostToExtensionWriter)

	mgr := cdp.NewManager(cdp.ManagerConfig{
		WSURL:       mockCDP.wsURL,
		Mapper:      relay,
		Broadcaster: relay,
	})
	_ = mgr.Connect(context.Background())
	relay.SetCDPManager(mgr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = relay.Serve(ctx) }()
	waitForSocket(t, socketPath)

	// Extension responder for getTargetMapping
	go func() {
		for {
			payload, err := wire.ReadFrame(hostToExtensionReader, wire.DefaultMaxFrame)
			if err != nil {
				return
			}
			var req map[string]any
			_ = json.Unmarshal(payload, &req)
			method, _ := req["method"].(string)
			id, _ := req["id"].(string)

			if method == "getTargetMapping" {
				_ = wire.WriteFrame(extensionToHostWriter, []byte(`{"jsonrpc":"2.0","id":"`+id+`","result":{"tabId":101,"targetId":"target-101"}}`))
			} else {
				_ = wire.WriteFrame(extensionToHostWriter, []byte(`{"jsonrpc":"2.0","id":"`+id+`","result":{"ok":true}}`))
			}
		}
	}()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial unix: %v", err)
	}
	defer conn.Close()

	// 1. Attach in direct mode
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      "client-1",
		"method":  "attach",
		"params": map[string]any{
			"tabId":      101,
			"_cdpMode":   "direct",
			"session_id": "sess-1",
		},
	}
	payload, _ := json.Marshal(req)
	_ = wire.WriteFrame(conn, payload)

	respBytes, err := wire.ReadFrame(conn, wire.DefaultMaxFrame)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	var resp map[string]any
	_ = json.Unmarshal(respBytes, &resp)
	if resp["error"] != nil {
		t.Fatalf("attach error: %v", resp["error"])
	}

	// 2. Execute CDP on that tab
	req2 := map[string]any{
		"jsonrpc": "2.0",
		"id":      "client-2",
		"method":  "executeCdp",
		"params": map[string]any{
			"target":        map[string]any{"tabId": 101},
			"method":        "Runtime.evaluate",
			"commandParams": map[string]any{"expression": "1+1"},
			"session_id":    "sess-1",
		},
	}
	payload2, _ := json.Marshal(req2)
	_ = wire.WriteFrame(conn, payload2)

	respBytes2, err := wire.ReadFrame(conn, wire.DefaultMaxFrame)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	var resp2 map[string]any
	_ = json.Unmarshal(respBytes2, &resp2)
	if resp2["error"] != nil {
		t.Fatalf("execute error: %v", resp2["error"])
	}
	if !strings.Contains(string(respBytes2), "eval-ok") {
		t.Errorf("expected eval-ok, got %s", string(respBytes2))
	}

	// 3. Detach in direct mode
	req3 := map[string]any{
		"jsonrpc": "2.0",
		"id":      "client-3",
		"method":  "detach",
		"params": map[string]any{
			"tabId":      101,
			"session_id": "sess-1",
		},
	}
	payload3, _ := json.Marshal(req3)
	_ = wire.WriteFrame(conn, payload3)

	respBytes3, err := wire.ReadFrame(conn, wire.DefaultMaxFrame)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	var resp3 map[string]any
	_ = json.Unmarshal(respBytes3, &resp3)
	if resp3["error"] != nil {
		t.Fatalf("detach error: %v", resp3["error"])
	}
}

func TestRelayCDPModeDirectUnavailableFails(t *testing.T) {
	extStdinR, extStdinW := io.Pipe()
	extStdoutR, extStdoutW := io.Pipe()
	defer extStdinR.Close()
	defer extStdinW.Close()
	defer extStdoutR.Close()
	defer extStdoutW.Close()

	socketDir := shortSocketDir(t)
	socketPath := filepath.Join(socketDir, "obu.sock")
	relay := NewRelay(Config{
		SocketDir:  socketDir,
		SocketPath: socketPath,
		CDPMode:    "direct",
	}, extStdinR, extStdoutW)
	// Do NOT set cdpManager; direct is unavailable

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = relay.Serve(ctx) }()
	waitForSocket(t, socketPath)

	go func() {
		for {
			payload, err := wire.ReadFrame(extStdoutR, wire.DefaultMaxFrame)
			if err != nil {
				return
			}
			var req map[string]any
			_ = json.Unmarshal(payload, &req)
			id, _ := req["id"].(string)
			_ = wire.WriteFrame(extStdinW, []byte(`{"jsonrpc":"2.0","id":"`+id+`","result":{"metadata":{"extensionId":"unknown","extensionInstanceId":"unknown"}}}`))
		}
	}()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      "client-fail",
		"method":  "attach",
		"params": map[string]any{
			"tabId":    101,
			"_cdpMode": "direct",
		},
	}
	payload, _ := json.Marshal(req)
	_ = wire.WriteFrame(conn, payload)

	respBytes, err := wire.ReadFrame(conn, wire.DefaultMaxFrame)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var resp map[string]any
	_ = json.Unmarshal(respBytes, &resp)
	if resp["error"] == nil {
		t.Fatal("expected error when direct mode is unavailable")
	}
	errObj := resp["error"].(map[string]any)
	if !strings.Contains(errObj["message"].(string), "direct CDP mode requested but") {
		t.Errorf("unexpected error message: %v", errObj["message"])
	}
}

func TestRelayCDPRoutePersistenceAndReleaseCleanup(t *testing.T) {
	hostToExtensionReader, hostToExtensionWriter := net.Pipe()
	defer hostToExtensionReader.Close()
	defer hostToExtensionWriter.Close()
	extensionToHostReader, extensionToHostWriter := net.Pipe()
	defer extensionToHostReader.Close()
	defer extensionToHostWriter.Close()

	mockCDP := setupRelayMockCDP(t)
	defer mockCDP.server.Close()

	socketDir := shortSocketDir(t)
	socketPath := filepath.Join(socketDir, "obu.sock")
	relay := NewRelay(Config{
		SocketDir:  socketDir,
		SocketPath: socketPath,
		CDPMode:    "auto",
	}, extensionToHostReader, hostToExtensionWriter)

	mgr := cdp.NewManager(cdp.ManagerConfig{
		WSURL:       mockCDP.wsURL,
		Mapper:      relay,
		Broadcaster: relay,
	})
	_ = mgr.Connect(context.Background())
	relay.SetCDPManager(mgr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = relay.Serve(ctx) }()
	waitForSocket(t, socketPath)

	var extensionReceivedMethods []string
	var extMu sync.Mutex

	// Extension responder
	go func() {
		for {
			payload, err := wire.ReadFrame(hostToExtensionReader, wire.DefaultMaxFrame)
			if err != nil {
				return
			}
			var req map[string]any
			_ = json.Unmarshal(payload, &req)
			method, _ := req["method"].(string)
			id, _ := req["id"].(string)

			extMu.Lock()
			extensionReceivedMethods = append(extensionReceivedMethods, method)
			extMu.Unlock()

			if method == "getTargetMapping" {
				_ = wire.WriteFrame(extensionToHostWriter, []byte(`{"jsonrpc":"2.0","id":"`+id+`","result":{"tabId":101,"targetId":"target-101"}}`))
			} else {
				_ = wire.WriteFrame(extensionToHostWriter, []byte(`{"jsonrpc":"2.0","id":"`+id+`","result":{"ok":true}}`))
			}
		}
	}()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial unix: %v", err)
	}
	defer conn.Close()

	// 1. Auto mode: attach tab 101 -> direct manager succeeds -> routed to direct
	req1 := map[string]any{
		"jsonrpc": "2.0",
		"id":      "c-1",
		"method":  "attach",
		"params":  map[string]any{"tabId": 101, "session_id": "sess-1"},
	}
	p1, _ := json.Marshal(req1)
	_ = wire.WriteFrame(conn, p1)
	respBytes, _ := wire.ReadFrame(conn, wire.DefaultMaxFrame)
	var resp1 map[string]any
	_ = json.Unmarshal(respBytes, &resp1)
	if resp1["error"] != nil {
		t.Fatalf("attach error: %v", resp1["error"])
	}

	// 2. Subsequent executeCdp for tab 101 must use direct route (not forwarded to extension!)
	req2 := map[string]any{
		"jsonrpc": "2.0",
		"id":      "c-2",
		"method":  "executeCdp",
		"params": map[string]any{
			"target":        map[string]any{"tabId": 101},
			"method":        "Runtime.evaluate",
			"commandParams": map[string]any{"expression": "2+2"},
			"session_id":    "sess-1",
		},
	}
	p2, _ := json.Marshal(req2)
	_ = wire.WriteFrame(conn, p2)
	respBytes2, _ := wire.ReadFrame(conn, wire.DefaultMaxFrame)
	var resp2 map[string]any
	_ = json.Unmarshal(respBytes2, &resp2)
	if resp2["error"] != nil {
		t.Fatalf("execute error: %v", resp2["error"])
	}

	extMu.Lock()
	for _, m := range extensionReceivedMethods {
		if m == "executeCdp" {
			t.Errorf("executeCdp should have been handled directly, not forwarded to extension")
		}
	}
	extMu.Unlock()

	// 3. releaseTabs: must clean up direct session AND forward releaseTabs to extension
	req3 := map[string]any{
		"jsonrpc": "2.0",
		"id":      "c-3",
		"method":  "releaseTabs",
		"params": map[string]any{
			"tabIds":     []int{101},
			"session_id": "sess-1",
		},
	}
	p3, _ := json.Marshal(req3)
	_ = wire.WriteFrame(conn, p3)
	respBytes3, _ := wire.ReadFrame(conn, wire.DefaultMaxFrame)
	var resp3 map[string]any
	_ = json.Unmarshal(respBytes3, &resp3)
	if resp3["error"] != nil {
		t.Fatalf("releaseTabs error: %v", resp3["error"])
	}

	extMu.Lock()
	foundRelease := false
	for _, m := range extensionReceivedMethods {
		if m == "releaseTabs" {
			foundRelease = true
			break
		}
	}
	extMu.Unlock()

	if !foundRelease {
		t.Errorf("releaseTabs should be forwarded to extension")
	}

	// Direct session for tab 101 must be gone
	if mgr.HasSession("sess-1", 101) {
		t.Errorf("direct session for tab 101 should have been cleaned up on releaseTabs")
	}
}

func TestRelayCDPDeadlinePropagationToTargetMapping(t *testing.T) {
	hostToExtensionReader, hostToExtensionWriter := net.Pipe()
	defer hostToExtensionReader.Close()
	defer hostToExtensionWriter.Close()
	extensionToHostReader, extensionToHostWriter := net.Pipe()
	defer extensionToHostReader.Close()
	defer extensionToHostWriter.Close()

	mockCDP := setupRelayMockCDP(t)
	defer mockCDP.server.Close()

	socketDir := shortSocketDir(t)
	socketPath := filepath.Join(socketDir, "obu.sock")
	relay := NewRelay(Config{
		SocketDir:  socketDir,
		SocketPath: socketPath,
		CDPMode:    "direct",
	}, extensionToHostReader, hostToExtensionWriter)

	mgr := cdp.NewManager(cdp.ManagerConfig{
		WSURL:       mockCDP.wsURL,
		Mapper:      relay,
		Broadcaster: relay,
	})
	_ = mgr.Connect(context.Background())
	relay.SetCDPManager(mgr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = relay.Serve(ctx) }()
	waitForSocket(t, socketPath)

	var capturedDeadline int64
	var extMu sync.Mutex

	go func() {
		for {
			payload, err := wire.ReadFrame(hostToExtensionReader, wire.DefaultMaxFrame)
			if err != nil {
				return
			}
			var req map[string]any
			_ = json.Unmarshal(payload, &req)
			method, _ := req["method"].(string)
			id, _ := req["id"].(string)
			params, _ := req["params"].(map[string]any)

			if method == "getTargetMapping" {
				extMu.Lock()
				if dl, ok := params["_deadlineUnixMs"].(float64); ok {
					capturedDeadline = int64(dl)
				}
				extMu.Unlock()
				_ = wire.WriteFrame(extensionToHostWriter, []byte(`{"jsonrpc":"2.0","id":"`+id+`","result":{"tabId":101,"targetId":"target-101"}}`))
			} else {
				_ = wire.WriteFrame(extensionToHostWriter, []byte(`{"jsonrpc":"2.0","id":"`+id+`","result":{"ok":true}}`))
			}
		}
	}()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	expectedDeadline := int64(1800000000000)
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      "c-deadline",
		"method":  "attach",
		"params": map[string]any{
			"tabId":           101,
			"session_id":      "sess-1",
			"_cdpMode":        "direct",
			"_deadlineUnixMs": expectedDeadline,
		},
	}
	p, _ := json.Marshal(req)
	_ = wire.WriteFrame(conn, p)
	_, _ = wire.ReadFrame(conn, wire.DefaultMaxFrame)

	extMu.Lock()
	gotDeadline := capturedDeadline
	extMu.Unlock()

	if gotDeadline != expectedDeadline {
		t.Errorf("expected getTargetMapping to receive _deadlineUnixMs = %d, got %d", expectedDeadline, gotDeadline)
	}
}

func TestRelayCDPClipboardDirectRouting(t *testing.T) {
	hostToExtensionReader, hostToExtensionWriter := net.Pipe()
	defer hostToExtensionReader.Close()
	defer hostToExtensionWriter.Close()
	extensionToHostReader, extensionToHostWriter := net.Pipe()
	defer extensionToHostReader.Close()
	defer extensionToHostWriter.Close()

	mockCDP := setupRelayMockCDP(t)
	defer mockCDP.server.Close()

	socketDir := shortSocketDir(t)
	socketPath := filepath.Join(socketDir, "obu.sock")
	relay := NewRelay(Config{
		SocketDir:  socketDir,
		SocketPath: socketPath,
		CDPMode:    "direct",
	}, extensionToHostReader, hostToExtensionWriter)

	mgr := cdp.NewManager(cdp.ManagerConfig{
		WSURL:       mockCDP.wsURL,
		Mapper:      relay,
		Broadcaster: relay,
	})
	_ = mgr.Connect(context.Background())
	relay.SetCDPManager(mgr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = relay.Serve(ctx) }()
	waitForSocket(t, socketPath)

	var extensionReceivedMethods []string
	var extMu sync.Mutex

	go func() {
		for {
			payload, err := wire.ReadFrame(hostToExtensionReader, wire.DefaultMaxFrame)
			if err != nil {
				return
			}
			var req map[string]any
			_ = json.Unmarshal(payload, &req)
			method, _ := req["method"].(string)
			id, _ := req["id"].(string)

			extMu.Lock()
			extensionReceivedMethods = append(extensionReceivedMethods, method)
			extMu.Unlock()

			if method == "getTargetMapping" {
				_ = wire.WriteFrame(extensionToHostWriter, []byte(`{"jsonrpc":"2.0","id":"`+id+`","result":{"tabId":101,"targetId":"target-101"}}`))
			} else {
				_ = wire.WriteFrame(extensionToHostWriter, []byte(`{"jsonrpc":"2.0","id":"`+id+`","result":{"ok":true}}`))
			}
		}
	}()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// 1. Attach tab 101 to direct mode
	req1 := map[string]any{
		"jsonrpc": "2.0",
		"id":      "c-attach",
		"method":  "attach",
		"params":  map[string]any{"tabId": 101, "session_id": "sess-1", "_cdpMode": "direct"},
	}
	p1, _ := json.Marshal(req1)
	_ = wire.WriteFrame(conn, p1)
	_, _ = wire.ReadFrame(conn, wire.DefaultMaxFrame)

	// 2. Call readClipboardText -> must be handled by direct manager, not extension!
	req2 := map[string]any{
		"jsonrpc": "2.0",
		"id":      "c-clip",
		"method":  "readClipboardText",
		"params":  map[string]any{"tabId": 101, "session_id": "sess-1"},
	}
	p2, _ := json.Marshal(req2)
	_ = wire.WriteFrame(conn, p2)
	respBytes, _ := wire.ReadFrame(conn, wire.DefaultMaxFrame)
	var resp map[string]any
	_ = json.Unmarshal(respBytes, &resp)
	if resp["error"] != nil {
		t.Fatalf("readClipboardText error: %v", resp["error"])
	}

	extMu.Lock()
	for _, m := range extensionReceivedMethods {
		if m == "readClipboardText" || m == "readClipboard" {
			t.Errorf("clipboard helper should NOT be forwarded to extension in direct mode")
		}
	}
	extMu.Unlock()
}

func TestRelayCDPCleanupWithNilManagerNeverInitializesDirect(t *testing.T) {
	hostToExtensionReader, hostToExtensionWriter := net.Pipe()
	defer hostToExtensionReader.Close()
	defer hostToExtensionWriter.Close()
	extensionToHostReader, extensionToHostWriter := net.Pipe()
	defer extensionToHostReader.Close()
	defer extensionToHostWriter.Close()

	socketDir := shortSocketDir(t)
	socketPath := filepath.Join(socketDir, "obu.sock")
	relay := NewRelay(Config{
		SocketDir:  socketDir,
		SocketPath: socketPath,
		CDPMode:    "auto",
	}, extensionToHostReader, hostToExtensionWriter)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = relay.Serve(ctx) }()
	waitForSocket(t, socketPath)

	var extensionReceivedMethods []string
	var extMu sync.Mutex

	go func() {
		for {
			payload, err := wire.ReadFrame(hostToExtensionReader, wire.DefaultMaxFrame)
			if err != nil {
				return
			}
			var req map[string]any
			_ = json.Unmarshal(payload, &req)
			method, _ := req["method"].(string)
			id, _ := req["id"].(string)

			extMu.Lock()
			extensionReceivedMethods = append(extensionReceivedMethods, method)
			extMu.Unlock()

			_ = wire.WriteFrame(extensionToHostWriter, []byte(`{"jsonrpc":"2.0","id":"`+id+`","result":{"ok":true}}`))
		}
	}()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Call releaseTabs when direct manager was never used/initialized
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      "c-rel",
		"method":  "releaseTabs",
		"params":  map[string]any{"session_id": "sess-1"},
	}
	p, _ := json.Marshal(req)
	_ = wire.WriteFrame(conn, p)
	_, _ = wire.ReadFrame(conn, wire.DefaultMaxFrame)

	extMu.Lock()
	methods := extensionReceivedMethods
	extMu.Unlock()

	for _, m := range methods {
		if m == "getInfo" {
			t.Errorf("cleanup with nil manager must NEVER emit internal getInfo or start direct initialization")
		}
	}

	if mgr := relay.getExistingCDPManager(); mgr != nil {
		t.Errorf("cleanup must not initialize direct CDP manager")
	}
}

func TestRelayCDPStickyRouteAcrossDetach(t *testing.T) {
	hostToExtensionReader, hostToExtensionWriter := net.Pipe()
	defer hostToExtensionReader.Close()
	defer hostToExtensionWriter.Close()
	extensionToHostReader, extensionToHostWriter := net.Pipe()
	defer extensionToHostReader.Close()
	defer extensionToHostWriter.Close()

	mockCDP := setupRelayMockCDP(t)
	defer mockCDP.server.Close()

	socketDir := shortSocketDir(t)
	socketPath := filepath.Join(socketDir, "obu.sock")
	relay := NewRelay(Config{
		SocketDir:  socketDir,
		SocketPath: socketPath,
		CDPMode:    "auto",
	}, extensionToHostReader, hostToExtensionWriter)

	mgr := cdp.NewManager(cdp.ManagerConfig{
		WSURL:       mockCDP.wsURL,
		Mapper:      relay,
		Broadcaster: relay,
	})
	_ = mgr.Connect(context.Background())
	relay.SetCDPManager(mgr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = relay.Serve(ctx) }()
	waitForSocket(t, socketPath)

	var extensionReceivedMethods []string
	var extMu sync.Mutex

	go func() {
		for {
			payload, err := wire.ReadFrame(hostToExtensionReader, wire.DefaultMaxFrame)
			if err != nil {
				return
			}
			var req map[string]any
			_ = json.Unmarshal(payload, &req)
			method, _ := req["method"].(string)
			id, _ := req["id"].(string)

			extMu.Lock()
			extensionReceivedMethods = append(extensionReceivedMethods, method)
			extMu.Unlock()

			if method == "getTargetMapping" {
				_ = wire.WriteFrame(extensionToHostWriter, []byte(`{"jsonrpc":"2.0","id":"`+id+`","result":{"tabId":101,"targetId":"target-101"}}`))
			} else {
				_ = wire.WriteFrame(extensionToHostWriter, []byte(`{"jsonrpc":"2.0","id":"`+id+`","result":{"ok":true}}`))
			}
		}
	}()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// 1. Attach tab 101 in auto mode -> direct route
	req1 := map[string]any{
		"jsonrpc": "2.0",
		"id":      "c-1",
		"method":  "attach",
		"params":  map[string]any{"tabId": 101, "session_id": "sess-1"},
	}
	p1, _ := json.Marshal(req1)
	_ = wire.WriteFrame(conn, p1)
	_, _ = wire.ReadFrame(conn, wire.DefaultMaxFrame)

	// 2. Detach tab 101
	req2 := map[string]any{
		"jsonrpc": "2.0",
		"id":      "c-2",
		"method":  "detach",
		"params":  map[string]any{"tabId": 101, "session_id": "sess-1"},
	}
	p2, _ := json.Marshal(req2)
	_ = wire.WriteFrame(conn, p2)
	_, _ = wire.ReadFrame(conn, wire.DefaultMaxFrame)

	// Route must remain sticky in direct mode
	relay.tabRoutesMu.RLock()
	route := relay.tabRoutes[cdp.SessionKey{SessionID: "sess-1", TabID: 101}]
	relay.tabRoutesMu.RUnlock()

	if route != "direct" {
		t.Fatalf("route must remain direct after detach, got %q", route)
	}

	// 3. Re-execute CDP on tab 101 without explicit re-attach: must continue using direct route
	req3 := map[string]any{
		"jsonrpc": "2.0",
		"id":      "c-3",
		"method":  "executeCdp",
		"params": map[string]any{
			"target":        map[string]any{"tabId": 101},
			"method":        "Runtime.evaluate",
			"commandParams": map[string]any{"expression": "40 + 2"},
			"session_id":    "sess-1",
		},
	}
	p3, _ := json.Marshal(req3)
	_ = wire.WriteFrame(conn, p3)
	respBytes, _ := wire.ReadFrame(conn, wire.DefaultMaxFrame)
	var resp3 map[string]any
	_ = json.Unmarshal(respBytes, &resp3)
	if resp3["error"] != nil {
		t.Fatalf("executeCdp after detach failed: %v", resp3["error"])
	}

	extMu.Lock()
	for _, m := range extensionReceivedMethods {
		if m == "executeCdp" {
			t.Errorf("executeCdp after detach should continue on direct route, not be forwarded to extension")
		}
	}
	extMu.Unlock()
}

func TestRelayCDPSetFileChooserFilesAutoModeProbeDoesNotInitializeDirect(t *testing.T) {
	hostToExtensionReader, hostToExtensionWriter := net.Pipe()
	defer hostToExtensionReader.Close()
	defer hostToExtensionWriter.Close()
	extensionToHostReader, extensionToHostWriter := net.Pipe()
	defer extensionToHostReader.Close()
	defer extensionToHostWriter.Close()

	socketDir := shortSocketDir(t)
	socketPath := filepath.Join(socketDir, "obu.sock")
	relay := NewRelay(Config{
		SocketDir:  socketDir,
		SocketPath: socketPath,
		CDPMode:    "auto",
	}, extensionToHostReader, hostToExtensionWriter)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = relay.Serve(ctx) }()
	waitForSocket(t, socketPath)

	var extensionReceivedMethods []string
	var extMu sync.Mutex

	go func() {
		for {
			payload, err := wire.ReadFrame(hostToExtensionReader, wire.DefaultMaxFrame)
			if err != nil {
				return
			}
			var req map[string]any
			_ = json.Unmarshal(payload, &req)
			method, _ := req["method"].(string)
			id, _ := req["id"].(string)

			extMu.Lock()
			extensionReceivedMethods = append(extensionReceivedMethods, method)
			extMu.Unlock()

			_ = wire.WriteFrame(extensionToHostWriter, []byte(`{"jsonrpc":"2.0","id":"`+id+`","result":{"ok":true}}`))
		}
	}()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Calling setFileChooserFiles in auto mode when manager is nil should forward to extension without probing direct init
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      "c-fc",
		"method":  "setFileChooserFiles",
		"params": map[string]any{
			"fileChooserId": "ext-chooser-123",
			"files":         []string{"/path/to/test.txt"},
			"session_id":    "sess-1",
		},
	}
	p, _ := json.Marshal(req)
	_ = wire.WriteFrame(conn, p)
	_, _ = wire.ReadFrame(conn, wire.DefaultMaxFrame)

	extMu.Lock()
	methods := extensionReceivedMethods
	extMu.Unlock()

	foundSetFiles := false
	for _, m := range methods {
		if m == "getInfo" {
			t.Errorf("setFileChooserFiles for extension chooser must not call getInfo or initialize direct CDP")
		}
		if m == "setFileChooserFiles" {
			foundSetFiles = true
		}
	}
	if !foundSetFiles {
		t.Errorf("setFileChooserFiles should be forwarded to extension")
	}
}

func TestEnsureCDPManagerKeepsOnePatientPendingHandshake(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	var hitsMu sync.Mutex
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitsMu.Lock()
		hits++
		hitsMu.Unlock()
		started <- struct{}{}
		<-release
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	tcpAddr, ok := server.Listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener addr = %T, want *net.TCPAddr", server.Listener.Addr())
	}
	profileRoot := t.TempDir()
	activePort := fmt.Sprintf("%d\n/devtools/browser/patient-test\n", tcpAddr.Port)
	if err := os.WriteFile(filepath.Join(profileRoot, "DevToolsActivePort"), []byte(activePort), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OBU_USER_DATA_DIR", profileRoot)

	relay := NewRelay(Config{CDPMode: "direct"}, strings.NewReader(""), io.Discard)
	errCh := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
			defer cancel()
			_, err := relay.ensureCDPManager(ctx)
			errCh <- err
		}()
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("patient CDP handshake never reached test server")
	}
	for i := 0; i < 2; i++ {
		if err := <-errCh; !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("caller %d error = %v, want deadline exceeded", i, err)
		}
	}

	hitsMu.Lock()
	gotHits := hits
	hitsMu.Unlock()
	if gotHits != 1 {
		t.Fatalf("pending browser handshakes = %d, want exactly 1", gotHits)
	}

	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	mgr, err := relay.ensureCDPManager(ctx)
	if err != nil {
		t.Fatalf("reuse approved pending handshake: %v", err)
	}
	if mgr == nil {
		t.Fatal("reuse approved pending handshake returned nil manager")
	}
	defer mgr.Close()

	hitsMu.Lock()
	gotHits = hits
	hitsMu.Unlock()
	if gotHits != 1 {
		t.Fatalf("approved manager opened %d browser handshakes, want 1", gotHits)
	}
}
