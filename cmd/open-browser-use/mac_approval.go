package main

import (
	"strings"
	"time"
)

// macApprovalStartDelay is how long a direct-capable request may stay
// unanswered before the approval helper is started. Once Chrome has approved
// the shared Browser Root connection these requests answer well within this
// window, so the helper (and its Accessibility polling) is only started when
// a request is actually parked behind Chrome's remote-debugging sheet.
const macApprovalStartDelay = 400 * time.Millisecond

// startMacApprovalHelperFunc is replaced in tests.
var startMacApprovalHelperFunc = startMacApprovalHelper

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

// scheduleMacApprovalHelper starts the approval helper if the request is
// still unanswered after macApprovalStartDelay. The returned stop function
// must be called once the response (or an error) arrives.
func scheduleMacApprovalHelper(reason string) (stop func()) {
	timer := time.AfterFunc(macApprovalStartDelay, func() {
		startMacApprovalHelperFunc(reason)
	})
	return func() { timer.Stop() }
}

// macApprovalHelperStatus maps the helper's exit code contract to a stable
// diagnostic status (see scripts/macos/open-browser-use-mac-approval-helper.m).
func macApprovalHelperStatus(exitCode int) string {
	switch exitCode {
	case 0:
		return "approved"
	case 1:
		return "sheet_not_found"
	case 2:
		return "usage_error"
	case 3:
		return "permission_denied"
	case 4:
		return "already_running"
	case 5:
		return "caller_exited"
	default:
		return "failed"
	}
}
