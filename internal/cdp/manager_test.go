package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type mockMapper struct {
	mapping map[int]string
	calls   []int
	mu      sync.Mutex
}

func (m *mockMapper) GetTargetMapping(ctx context.Context, sessionID, turnID string, tabID int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, tabID)
	if targetID, ok := m.mapping[tabID]; ok {
		return targetID, nil
	}
	return "", errors.New("tab not found in mock mapper")
}

type mockBroadcaster struct {
	events []map[string]any
	mu     sync.Mutex
}

func (b *mockBroadcaster) BroadcastNotification(method string, params any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, map[string]any{
		"method": method,
		"params": params,
	})
}

func setupMockCDPServer(t *testing.T, handler func(msg map[string]any, send func(v any))) (*httptest.Server, string) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		var sendMu sync.Mutex
		send := func(v any) {
			sendMu.Lock()
			defer sendMu.Unlock()
			payload, _ := json.Marshal(v)
			_ = conn.WriteMessage(websocket.TextMessage, payload)
		}

		for {
			_, msgBytes, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg map[string]any
			if err := json.Unmarshal(msgBytes, &msg); err != nil {
				continue
			}
			handler(msg, send)
		}
	}))

	wsURL := "ws://" + server.Listener.Addr().String() + "/devtools/browser/test-guid"
	return server, wsURL
}

func TestManagerConnectionObserverTracksRootSocket(t *testing.T) {
	server, wsURL := setupMockCDPServer(t, func(msg map[string]any, send func(v any)) {})
	defer server.Close()

	states := make(chan bool, 4)
	mgr := NewManager(ManagerConfig{
		WSURL: wsURL,
		ConnectionObserver: func(connected bool) {
			states <- connected
		},
	})
	if err := mgr.Connect(context.Background()); err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	select {
	case connected := <-states:
		if !connected {
			t.Fatal("expected connected=true after root CDP WebSocket opens")
		}
	case <-time.After(time.Second):
		t.Fatal("missing connected=true notification")
	}

	if err := mgr.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
	select {
	case connected := <-states:
		if connected {
			t.Fatal("expected connected=false when root CDP WebSocket closes")
		}
	case <-time.After(time.Second):
		t.Fatal("missing connected=false notification")
	}
}

func TestManagerAttachAndWake(t *testing.T) {
	var commands []string
	var mu sync.Mutex

	server, wsURL := setupMockCDPServer(t, func(msg map[string]any, send func(v any)) {
		method, _ := msg["method"].(string)
		id := msg["id"]
		mu.Lock()
		commands = append(commands, method)
		mu.Unlock()

		switch method {
		case "Target.attachToTarget":
			send(map[string]any{
				"id":     id,
				"result": map[string]any{"sessionId": "cdp-sess-101"},
			})
		case "Page.setWebLifecycleState":
			send(map[string]any{
				"id":     id,
				"result": map[string]any{},
			})
		case "Emulation.setFocusEmulationEnabled":
			send(map[string]any{
				"id":     id,
				"result": map[string]any{},
			})
		}
	})
	defer server.Close()

	mapper := &mockMapper{mapping: map[int]string{101: "target-101"}}
	broadcaster := &mockBroadcaster{}
	mgr := NewManager(ManagerConfig{
		WSURL:       wsURL,
		Mapper:      mapper,
		Broadcaster: broadcaster,
	})

	err := mgr.Connect(context.Background())
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer mgr.Close()

	wakeRes, err := mgr.Attach(context.Background(), "sess-1", "turn-1", 101)
	if err != nil {
		t.Fatalf("attach failed: %v", err)
	}
	if wakeRes == nil {
		t.Fatal("expected wake result")
	}

	mu.Lock()
	defer mu.Unlock()
	expected := []string{"Target.attachToTarget", "Page.setWebLifecycleState", "Emulation.setFocusEmulationEnabled"}
	if len(commands) != 3 {
		t.Fatalf("expected 3 commands, got %v", commands)
	}
	for i, exp := range expected {
		if commands[i] != exp {
			t.Errorf("command %d: expected %s, got %s", i, exp, commands[i])
		}
	}
}

func TestManagerExecuteCdp(t *testing.T) {
	server, wsURL := setupMockCDPServer(t, func(msg map[string]any, send func(v any)) {
		method, _ := msg["method"].(string)
		id := msg["id"]
		sessID, _ := msg["sessionId"].(string)

		if method == "Target.attachToTarget" {
			send(map[string]any{
				"id":     id,
				"result": map[string]any{"sessionId": "cdp-sess-101"},
			})
		} else if method == "Runtime.evaluate" {
			if sessID != "cdp-sess-101" {
				t.Errorf("expected sessionId cdp-sess-101, got %s", sessID)
			}
			send(map[string]any{
				"id":     id,
				"result": map[string]any{"result": map[string]any{"value": 42}},
			})
		} else if method == "Browser.getVersion" {
			if sessID != "" {
				t.Errorf("expected empty sessionId for browser command, got %s", sessID)
			}
			send(map[string]any{
				"id":     id,
				"result": map[string]any{"protocolVersion": "1.3"},
			})
		} else {
			send(map[string]any{"id": id, "result": map[string]any{}})
		}
	})
	defer server.Close()

	mapper := &mockMapper{mapping: map[int]string{101: "target-101"}}
	mgr := NewManager(ManagerConfig{
		WSURL:  wsURL,
		Mapper: mapper,
	})

	err := mgr.Connect(context.Background())
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer mgr.Close()

	// 1. Lazy attach and execute
	res, err := mgr.ExecuteCdp(context.Background(), "sess-1", "turn-1", 101, "Runtime.evaluate", map[string]any{"expression": "21 + 21"}, 3*time.Second)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(string(res), "42") {
		t.Errorf("expected 42 in result, got %s", string(res))
	}

	// 2. Browser-wide command without tabId
	bRes, err := mgr.ExecuteCdp(context.Background(), "sess-1", "turn-1", 0, "Browser.getVersion", map[string]any{}, 3*time.Second)
	if err != nil {
		t.Fatalf("browser execute failed: %v", err)
	}
	if !strings.Contains(string(bRes), "1.3") {
		t.Errorf("expected 1.3 in result, got %s", string(bRes))
	}
}

func TestManagerEventTranslation(t *testing.T) {
	var sendEvent func(v any)
	var sendMu sync.Mutex
	server, wsURL := setupMockCDPServer(t, func(msg map[string]any, send func(v any)) {
		method, _ := msg["method"].(string)
		id := msg["id"]
		sendMu.Lock()
		sendEvent = send
		sendMu.Unlock()

		if method == "Target.attachToTarget" {
			send(map[string]any{
				"id":     id,
				"result": map[string]any{"sessionId": "cdp-sess-101"},
			})
		} else {
			send(map[string]any{"id": id, "result": map[string]any{}})
		}
	})
	defer server.Close()

	mapper := &mockMapper{mapping: map[int]string{101: "target-101"}}
	broadcaster := &mockBroadcaster{}
	mgr := NewManager(ManagerConfig{
		WSURL:       wsURL,
		Mapper:      mapper,
		Broadcaster: broadcaster,
	})

	err := mgr.Connect(context.Background())
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer mgr.Close()

	_, err = mgr.Attach(context.Background(), "sess-1", "turn-1", 101)
	if err != nil {
		t.Fatalf("attach failed: %v", err)
	}

	// Simulate event from Chrome on that session
	sendMu.Lock()
	send := sendEvent
	sendMu.Unlock()
	send(map[string]any{
		"sessionId": "cdp-sess-101",
		"method":    "Page.loadEventFired",
		"params":    map[string]any{"timestamp": 12345.67},
	})

	time.Sleep(50 * time.Millisecond)

	broadcaster.mu.Lock()
	defer broadcaster.mu.Unlock()
	if len(broadcaster.events) == 0 {
		t.Fatal("expected broadcasted event")
	}
	ev := broadcaster.events[0]
	if ev["method"] != "onCDPEvent" {
		t.Errorf("expected onCDPEvent, got %v", ev["method"])
	}
	params, _ := ev["params"].(map[string]any)
	source, _ := params["source"].(map[string]any)
	if source["tabId"] != 101 {
		t.Errorf("expected source.tabId = 101, got %v", source["tabId"])
	}
	if _, hasTarget := source["targetId"]; hasTarget {
		t.Errorf("source should not contain targetId for tab-scoped events, got %v", source)
	}
}

func TestManagerDetachAndClean(t *testing.T) {
	var detachedSessions []string
	var mu sync.Mutex

	server, wsURL := setupMockCDPServer(t, func(msg map[string]any, send func(v any)) {
		method, _ := msg["method"].(string)
		id := msg["id"]

		if method == "Target.attachToTarget" {
			params, _ := msg["params"].(map[string]any)
			tid, _ := params["targetId"].(string)
			send(map[string]any{
				"id":     id,
				"result": map[string]any{"sessionId": "sess-" + tid},
			})
		} else if method == "Target.detachFromTarget" {
			params, _ := msg["params"].(map[string]any)
			sid, _ := params["sessionId"].(string)
			mu.Lock()
			detachedSessions = append(detachedSessions, sid)
			mu.Unlock()
			send(map[string]any{"id": id, "result": map[string]any{}})
		} else {
			send(map[string]any{"id": id, "result": map[string]any{}})
		}
	})
	defer server.Close()

	mapper := &mockMapper{mapping: map[int]string{
		101: "t101",
		102: "t102",
		103: "t103",
	}}
	mgr := NewManager(ManagerConfig{
		WSURL:  wsURL,
		Mapper: mapper,
	})

	err := mgr.Connect(context.Background())
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer mgr.Close()

	_, _ = mgr.Attach(context.Background(), "sess-1", "turn-1", 101)
	_, _ = mgr.Attach(context.Background(), "sess-1", "turn-1", 102)
	_, _ = mgr.Attach(context.Background(), "sess-1", "turn-1", 103)

	// Detach single tab
	err = mgr.Detach(context.Background(), "sess-1", 101)
	if err != nil {
		t.Fatalf("detach failed: %v", err)
	}

	mu.Lock()
	if len(detachedSessions) != 1 || detachedSessions[0] != "sess-t101" {
		t.Errorf("expected detached sess-t101, got %v", detachedSessions)
	}
	mu.Unlock()

	// Subset clean
	mgr.CleanSessionsForSession(context.Background(), "sess-1", []int{102})
	mu.Lock()
	if len(detachedSessions) != 2 || detachedSessions[1] != "sess-t102" {
		t.Errorf("expected detached sess-t102, got %v", detachedSessions)
	}
	mu.Unlock()

	// All clean for session
	mgr.CleanSessionsForSession(context.Background(), "sess-1", nil)
	mu.Lock()
	if len(detachedSessions) != 3 || detachedSessions[2] != "sess-t103" {
		t.Errorf("expected detached sess-t103, got %v", detachedSessions)
	}
	mu.Unlock()
}

func TestManagerDelayedAttachQuarantineDetached(t *testing.T) {
	detachedCh := make(chan string, 1)

	server, wsURL := setupMockCDPServer(t, func(msg map[string]any, send func(v any)) {
		method, _ := msg["method"].(string)
		id := msg["id"]

		if method == "Target.attachToTarget" {
			// Intentionally delay replying until after caller attach timeout
			go func() {
				time.Sleep(120 * time.Millisecond)
				send(map[string]any{
					"id":     id,
					"result": map[string]any{"sessionId": "cdp-sess-late"},
				})
			}()
		} else if method == "Target.detachFromTarget" {
			params, _ := msg["params"].(map[string]any)
			sid, _ := params["sessionId"].(string)
			send(map[string]any{"id": id, "result": map[string]any{}})
			select {
			case detachedCh <- sid:
			default:
			}
		}
	})
	defer server.Close()

	mapper := &mockMapper{mapping: map[int]string{101: "target-101"}}
	mgr := NewManager(ManagerConfig{
		WSURL:         wsURL,
		Mapper:        mapper,
		AttachTimeout: 40 * time.Millisecond,
	})

	err := mgr.Connect(context.Background())
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer mgr.Close()

	// Attach should time out
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err = mgr.Attach(ctx, "sess-1", "turn-1", 101)
	if err == nil {
		t.Fatal("expected attach to time out locally")
	}

	// Wait for the late response to arrive and trigger Target.detachFromTarget
	select {
	case sid := <-detachedCh:
		if sid != "cdp-sess-late" {
			t.Errorf("expected detached session cdp-sess-late, got %s", sid)
		}
	case <-time.After(time.Second):
		t.Fatal("late attach did not trigger Target.detachFromTarget within 1s")
	}
}

func TestManagerFileChooserLifecycleAndValidation(t *testing.T) {
	var domFileInputCommands []map[string]any
	var interceptDisabled bool
	var mu sync.Mutex
	var sendEvent func(v any)

	server, wsURL := setupMockCDPServer(t, func(msg map[string]any, send func(v any)) {
		method, _ := msg["method"].(string)
		id := msg["id"]
		params, _ := msg["params"].(map[string]any)
		mu.Lock()
		sendEvent = send
		mu.Unlock()

		if method == "Target.attachToTarget" {
			send(map[string]any{"id": id, "result": map[string]any{"sessionId": "cdp-sess-101"}})
		} else if method == "Page.setInterceptFileChooserDialog" {
			if enabled, ok := params["enabled"].(bool); ok && !enabled {
				mu.Lock()
				interceptDisabled = true
				mu.Unlock()
			}
			send(map[string]any{"id": id, "result": map[string]any{}})
		} else if method == "DOM.setFileInputFiles" {
			mu.Lock()
			domFileInputCommands = append(domFileInputCommands, params)
			mu.Unlock()
			send(map[string]any{"id": id, "result": map[string]any{}})
		} else {
			send(map[string]any{"id": id, "result": map[string]any{}})
		}
	})
	defer server.Close()

	mapper := &mockMapper{mapping: map[int]string{101: "target-101"}}
	mgr := NewManager(ManagerConfig{
		WSURL:  wsURL,
		Mapper: mapper,
	})

	err := mgr.Connect(context.Background())
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer mgr.Close()

	// 1. Wait for file chooser (in a goroutine) and emit Page.fileChooserOpened with backendNodeId and selectSingle
	fcResultCh := make(chan map[string]any, 1)
	go func() {
		res, err := mgr.WaitForFileChooser(context.Background(), "sess-1", "turn-1", 101, 2*time.Second)
		if err != nil {
			t.Errorf("WaitForFileChooser error: %v", err)
			return
		}
		fcResultCh <- res
	}()

	time.Sleep(50 * time.Millisecond)

	// Simulate Chrome emitting Page.fileChooserOpened
	mu.Lock()
	sendFn := sendEvent
	mu.Unlock()
	sendFn(map[string]any{
		"sessionId": "cdp-sess-101",
		"method":    "Page.fileChooserOpened",
		"params": map[string]any{
			"mode":          "selectSingle",
			"backendNodeId": 42,
		},
	})

	var fcRes map[string]any
	select {
	case fcRes = <-fcResultCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for file chooser result")
	}

	chooserID, ok := fcRes["fileChooserId"].(string)
	if !ok || chooserID == "" {
		t.Fatalf("invalid fileChooserId: %v", fcRes)
	}
	if fcRes["mode"] != "selectSingle" {
		t.Errorf("expected mode selectSingle, got %v", fcRes["mode"])
	}

	// 2. Intercept disable must have been called in defer
	mu.Lock()
	disabled := interceptDisabled
	mu.Unlock()
	if !disabled {
		t.Error("expected Page.setInterceptFileChooserDialog enabled:false to be called in defer")
	}

	// 3. Validation: session isolation (reject different session)
	_, err = mgr.SetFileChooserFiles(context.Background(), "other-session", chooserID, []string{"/path/to/file.png"})
	if err == nil {
		t.Fatal("expected error when setting files from different session")
	}

	// 4. Validation: reject empty file array
	_, err = mgr.SetFileChooserFiles(context.Background(), "sess-1", chooserID, []string{})
	if err == nil {
		t.Fatal("expected error for empty files slice")
	}

	// 5. Validation: reject empty strings in file array
	_, err = mgr.SetFileChooserFiles(context.Background(), "sess-1", chooserID, []string{""})
	if err == nil {
		t.Fatal("expected error for empty string in files slice")
	}

	// 6. Validation: selectSingle rejects multiple files
	_, err = mgr.SetFileChooserFiles(context.Background(), "sess-1", chooserID, []string{"/path/1.png", "/path/2.png"})
	if err == nil {
		t.Fatal("expected error when multiple files passed to selectSingle chooser")
	}

	// 7. Successful set file with backendNodeId preserved
	setRes, err := mgr.SetFileChooserFiles(context.Background(), "sess-1", chooserID, []string{"/path/to/valid.png"})
	if err != nil {
		t.Fatalf("SetFileChooserFiles failed: %v", err)
	}
	if setRes["ok"] != true {
		t.Errorf("expected ok: true, got %v", setRes)
	}

	mu.Lock()
	if len(domFileInputCommands) == 0 {
		t.Fatal("expected DOM.setFileInputFiles command")
	}
	domCmd := domFileInputCommands[0]
	mu.Unlock()

	rawNodeID := domCmd["backendNodeId"]
	var nodeID int
	if f, ok := rawNodeID.(float64); ok {
		nodeID = int(f)
	} else if i, ok := rawNodeID.(int); ok {
		nodeID = i
	}
	if nodeID != 42 {
		t.Errorf("expected backendNodeId = 42 in DOM.setFileInputFiles, got %v", rawNodeID)
	}

	// 8. Cleanup verification: detach removes chooser
	// Wait again to register another chooser
	go func() {
		_, _ = mgr.WaitForFileChooser(context.Background(), "sess-1", "turn-1", 101, 2*time.Second)
	}()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	sendFn2 := sendEvent
	mu.Unlock()
	sendFn2(map[string]any{
		"sessionId": "cdp-sess-101",
		"method":    "Page.fileChooserOpened",
		"params": map[string]any{
			"mode":          "selectMultiple",
			"backendNodeId": 99,
		},
	})
	time.Sleep(50 * time.Millisecond)

	// Detach tab 101
	_ = mgr.Detach(context.Background(), "sess-1", 101)
	mgr.fcMu.Lock()
	waiterCount := len(mgr.fcWaiters)
	activeCount := len(mgr.activeChooser)
	mgr.fcMu.Unlock()

	if waiterCount != 0 {
		t.Errorf("expected 0 waiters after detach, got %d", waiterCount)
	}
	if activeCount != 0 {
		t.Errorf("expected 0 active choosers after detach, got %d", activeCount)
	}
}

func TestManagerClipboardJSException(t *testing.T) {
	server, wsURL := setupMockCDPServer(t, func(msg map[string]any, send func(v any)) {
		method, _ := msg["method"].(string)
		id := msg["id"]

		if method == "Target.attachToTarget" {
			send(map[string]any{"id": id, "result": map[string]any{"sessionId": "cdp-sess-101"}})
		} else if method == "Runtime.evaluate" {
			// Simulate JS exception in evaluate (e.g. Permission denied)
			send(map[string]any{
				"id": id,
				"result": map[string]any{
					"result": map[string]any{"type": "undefined"},
					"exceptionDetails": map[string]any{
						"text": "Uncaught (in promise) DOMException: Read permission denied",
						"exception": map[string]any{
							"description": "DOMException: Read permission denied",
						},
					},
				},
			})
		} else {
			send(map[string]any{"id": id, "result": map[string]any{}})
		}
	})
	defer server.Close()

	mapper := &mockMapper{mapping: map[int]string{101: "target-101"}}
	mgr := NewManager(ManagerConfig{
		WSURL:  wsURL,
		Mapper: mapper,
	})

	err := mgr.Connect(context.Background())
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer mgr.Close()

	_, err = mgr.ReadClipboardText(context.Background(), "sess-1", "turn-1", 101)
	if err == nil {
		t.Fatal("expected error on JavaScript exceptionDetails")
	}
	if !strings.Contains(err.Error(), "Read permission denied") {
		t.Errorf("expected exception description in error, got: %v", err)
	}
}
