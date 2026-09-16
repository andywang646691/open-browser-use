package cdp

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestManagerExtensionLifecycle(t *testing.T) {
	type command struct {
		method string
		params map[string]any
	}
	var (
		commands       []command
		commandsMu     sync.Mutex
		targetGetCalls int
	)

	server, wsURL := setupMockCDPServer(t, func(msg map[string]any, send func(v any)) {
		method, _ := msg["method"].(string)
		params, _ := msg["params"].(map[string]any)
		commandsMu.Lock()
		commands = append(commands, command{method: method, params: params})
		commandsMu.Unlock()

		switch method {
		case "Extensions.getExtensions":
			send(map[string]any{"id": msg["id"], "result": map[string]any{
				"extensions": []any{map[string]any{
					"id": "ext-1", "name": "Test extension", "version": "1.2.3", "path": "/tmp/ext-1", "enabled": true,
				}},
			}})
		case "Extensions.loadUnpacked":
			send(map[string]any{"id": msg["id"], "result": map[string]any{"id": "ext-1"}})
		case "Extensions.triggerAction", "Target.setDiscoverTargets", "Target.detachFromTarget":
			send(map[string]any{"id": msg["id"], "result": map[string]any{}})
		case "Target.getTargets":
			targetGetCalls++
			// The trigger operation sees a synthetic tab target. Its page child
			// is attached through the tab session below.
			send(map[string]any{"id": msg["id"], "result": map[string]any{
				"targetInfos": []any{map[string]any{"targetId": "tab-1", "type": "tab", "url": "about:blank"}},
			}})
		case "Target.attachToTarget":
			send(map[string]any{"id": msg["id"], "result": map[string]any{"sessionId": "tab-session-1"}})
		case "Target.setAutoAttach":
			// The event's outer sessionId is the parent synthetic-tab session;
			// params.sessionId is the child page session. This is the exact
			// relationship needed by Extensions.triggerAction.
			send(map[string]any{
				"sessionId": "tab-session-1",
				"method":    "Target.attachedToTarget",
				"params": map[string]any{
					"sessionId":  "page-session-1",
					"targetInfo": map[string]any{"targetId": "target-1", "type": "page"},
				},
			})
			send(map[string]any{"id": msg["id"], "result": map[string]any{}})
		default:
			send(map[string]any{"id": msg["id"], "result": map[string]any{}})
		}
	})
	defer server.Close()

	manager := NewManager(ManagerConfig{WSURL: wsURL})
	if err := manager.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	got, err := manager.ListExtensions(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	want := []ExtensionInfo{{ID: "ext-1", Name: "Test extension", Version: "1.2.3", Path: "/tmp/ext-1", Enabled: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extensions = %#v, want %#v", got, want)
	}
	if id, err := manager.LoadUnpackedExtension(context.Background(), "/tmp/ext-1", true, time.Second); err != nil || id != "ext-1" {
		t.Fatalf("load unpacked = %q, %v", id, err)
	}
	if id, err := manager.ReloadExtension(context.Background(), "ext-1", time.Second); err != nil || id != "ext-1" {
		t.Fatalf("reload = %q, %v", id, err)
	}
	if err := manager.TriggerExtensionAction(context.Background(), "ext-1", "target-1", time.Second); err != nil {
		t.Fatal(err)
	}
	if err := manager.UninstallExtension(context.Background(), "ext-1", time.Second); err != nil {
		t.Fatal(err)
	}

	commandsMu.Lock()
	gotMethods := make([]string, len(commands))
	for i, item := range commands {
		gotMethods[i] = item.method
	}
	gotLoadParams := commands[1].params
	gotReloadLoadParams := commands[3].params
	commandsMu.Unlock()

	wantMethods := []string{
		"Extensions.getExtensions",
		"Extensions.loadUnpacked",
		"Extensions.getExtensions",
		"Extensions.loadUnpacked",
		"Target.setDiscoverTargets",
		"Target.getTargets",
		"Target.attachToTarget",
		"Target.setAutoAttach",
		"Extensions.triggerAction",
		"Target.detachFromTarget",
		"Extensions.uninstall",
	}
	if !reflect.DeepEqual(gotMethods, wantMethods) {
		t.Fatalf("methods = %#v, want %#v", gotMethods, wantMethods)
	}
	if gotLoadParams["path"] != "/tmp/ext-1" || gotLoadParams["enableInIncognito"] != true {
		t.Fatalf("load params = %#v, want path and enableInIncognito", gotLoadParams)
	}
	if gotReloadLoadParams["path"] != "/tmp/ext-1" {
		t.Fatalf("reload load params = %#v, want path", gotReloadLoadParams)
	}
	if _, present := gotReloadLoadParams["enableInIncognito"]; present {
		t.Fatalf("reload load params should omit false enableInIncognito: %#v", gotReloadLoadParams)
	}
}

func TestManagerReloadMissingExtension(t *testing.T) {
	server, wsURL := setupMockCDPServer(t, func(msg map[string]any, send func(v any)) {
		send(map[string]any{"id": msg["id"], "result": map[string]any{"extensions": []any{}}})
	})
	defer server.Close()

	manager := NewManager(ManagerConfig{WSURL: wsURL})
	if err := manager.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	if _, err := manager.ReloadExtension(context.Background(), "missing", time.Second); err == nil {
		t.Fatal("expected reload of missing extension to fail")
	}
}
