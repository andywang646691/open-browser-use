package cdp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestClientSendWithLateHandlerRaceBoundary(t *testing.T) {
	t.Run("response claimed by readLoop right at deadline returns normal response", func(t *testing.T) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				var req map[string]any
				if err := json.Unmarshal(msgBytes, &req); err != nil {
					continue
				}
				id := req["id"]
				// Immediate reply
				_ = conn.WriteJSON(map[string]any{
					"id":     id,
					"result": map[string]any{"status": "claimed-in-time"},
				})
			}
		}))
		defer server.Close()

		wsURL := "ws://" + server.Listener.Addr().String() + "/devtools/browser/test"
		client, err := Dial(context.Background(), wsURL, nil)
		if err != nil {
			t.Fatalf("dial failed: %v", err)
		}
		defer client.Close()

		var lateCalled bool
		var lateMu sync.Mutex
		lateHandler := func(res json.RawMessage, err *CDPError) {
			lateMu.Lock()
			lateCalled = true
			lateMu.Unlock()
		}

		// A very tight timeout where response arrives right around deadline
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		res, err := client.SendWithLateHandler(ctx, "Test.method", map[string]any{}, "", lateHandler)
		if err == nil {
			if !strings.Contains(string(res), "claimed-in-time") {
				t.Errorf("expected status claimed-in-time, got %s", string(res))
			}
		}

		// Wait a bit to ensure late handler was not erroneously invoked
		time.Sleep(50 * time.Millisecond)
		lateMu.Lock()
		if lateCalled {
			t.Error("late handler should not be invoked when response was claimed in time")
		}
		lateMu.Unlock()
	})

	t.Run("truly timed out request installs late handler and invokes it on late arrival", func(t *testing.T) {
		var serverSend func(v any)
		var serverMu sync.Mutex

		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()

			serverMu.Lock()
			serverSend = func(v any) {
				_ = conn.WriteJSON(v)
			}
			serverMu.Unlock()

			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}))
		defer server.Close()

		wsURL := "ws://" + server.Listener.Addr().String() + "/devtools/browser/test"
		client, err := Dial(context.Background(), wsURL, nil)
		if err != nil {
			t.Fatalf("dial failed: %v", err)
		}
		defer client.Close()

		lateCh := make(chan string, 1)
		lateHandler := func(res json.RawMessage, err *CDPError) {
			var parsed struct {
				LateValue string `json:"lateValue"`
			}
			_ = json.Unmarshal(res, &parsed)
			lateCh <- parsed.LateValue
		}

		// Times out after 10ms
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		_, err = client.SendWithLateHandler(ctx, "Target.attachToTarget", map[string]any{}, "", lateHandler)
		if err == nil {
			t.Fatal("expected timeout error")
		}

		// Now simulate late response from server
		time.Sleep(30 * time.Millisecond)
		serverMu.Lock()
		send := serverSend
		serverMu.Unlock()
		if send != nil {
			send(map[string]any{
				"id":     1,
				"result": map[string]any{"lateValue": "late-attached-session"},
			})
		}

		select {
		case val := <-lateCh:
			if val != "late-attached-session" {
				t.Errorf("expected lateValue = late-attached-session, got %s", val)
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatal("late handler was not called within 500ms")
		}
	})
}
