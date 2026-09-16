package cdp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestManualExtensionLifecycleSmoke(t *testing.T) {
	userData := os.Getenv("OBU_MANUAL_EXTENSION_PROFILE")
	extPath := os.Getenv("OBU_MANUAL_EXTENSION_PATH")
	if userData == "" || extPath == "" {
		t.Skip("manual smoke environment not configured")
	}
	port, err := ReadDevToolsActivePort(userData)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	manager := NewManager(ManagerConfig{WSURL: port.WebSocketURL(), CDPTimeout: 5 * time.Second})
	if err := manager.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	id, err := manager.LoadUnpackedExtension(ctx, extPath, false, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.UninstallExtension(context.Background(), id, 5*time.Second)

	raw, err := manager.ExecuteCdp(ctx, "", "", 0, "Target.getTargets", map[string]any{"filter": []any{map[string]any{}}}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
			URL      string `json:"url"`
		} `json:"targetInfos"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	page := ""
	for _, target := range response.TargetInfos {
		if target.Type == "page" && !strings.HasPrefix(target.URL, "chrome-extension://") {
			page = target.TargetID
			break
		}
	}
	if page == "" {
		t.Fatal("no page target")
	}
	if err := manager.TriggerExtensionAction(ctx, id, page, 5*time.Second); err != nil {
		t.Fatal(err)
	}
}
