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
		mode = strings.ToLower(strings.TrimSpace(envValue("OBU_CDP_MODE", "")))
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
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	go func() {
		defer cancel()
		_ = runMacRemoteDebuggingUIHelper(ctx, reason)
	}()
}
