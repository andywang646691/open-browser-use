package main

import (
	"context"
	"strings"
	"time"

	"github.com/ifuryst/open-browser-use/internal/diaglog"
)

func requestMayNeedDirectCDPApproval(method string, params map[string]any) bool {
	mode := ""
	if value, ok := params["_cdpMode"].(string); ok {
		mode = strings.ToLower(strings.TrimSpace(value))
	}
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(envValue("OBU_CDP_MODE", "extension")))
	}
	if mode == "" {
		mode = "extension"
	}
	switch method {
	case "listExtensions", "installExtension", "reloadExtension", "triggerExtensionAction", "uninstallExtension":
		// Extension lifecycle commands always use the browser-root CDP
		// Extensions domain, even when ordinary page CDP is configured to use
		// the extension route.
		return true
	}
	if mode == "extension" {
		return false
	}

	switch method {
	case "attach", "executeCdp", "readClipboardText", "writeClipboardText", "readClipboard", "writeClipboard", "waitForFileChooser", "setFileChooserFiles":
		return true
	default:
		return false
	}
}

func startMacRemoteDebuggingUIHelper(reason string) {
	diaglog.Event("mac_approval_helper_requested", map[string]any{
		"reason": reason,
	})
	// The Chrome sheet can be created after the Browser Root handshake starts.
	// Keep the helper alive longer than a normal caller deadline so a late sheet
	// is still approved even when the caller has already stopped waiting.
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	go func() {
		defer cancel()
		_ = runMacRemoteDebuggingUIHelper(ctx, reason)
	}()
}
