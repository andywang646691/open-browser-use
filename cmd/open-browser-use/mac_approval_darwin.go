//go:build darwin

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/ifuryst/open-browser-use/internal/diaglog"
)

const macApprovalHelperExecutable = "open-browser-use-mac-approval-helper"

func macApprovalHelperEnvValue() string {
	if path := strings.TrimSpace(os.Getenv("OBU_MAC_APPROVAL_HELPER")); path != "" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(home, "Library", "Application Support", "OpenBrowserUse", "approval-helper", "Open Browser Use Approval Helper.app", "Contents", "MacOS", macApprovalHelperExecutable)
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// startMacApprovalHelper launches one detached helper that approves the
// remote-debugging sheet and then closes the automation infobar. The helper
// owns the whole sequence in its own session, so it survives a one-shot CLI
// returning as soon as Chrome answers; it stops waiting for the sheet when
// this process exits (OBU_MAC_APPROVAL_PARENT_PID).
func startMacApprovalHelper(reason string) {
	helper := macApprovalHelperEnvValue()
	if helper == "" {
		diaglog.Event("mac_approval_helper_missing", map[string]any{"reason": reason})
		return
	}
	cmd := exec.Command(helper, "approve-then-dismiss-wait", "30", "15")
	cmd.Env = append(os.Environ(),
		"OBU_MAC_APPROVAL_REASON="+reason,
		"OBU_MAC_APPROVAL_PARENT_PID="+strconv.Itoa(os.Getpid()),
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		diaglog.Event("mac_approval_helper_start_failed", map[string]any{
			"path":   helper,
			"reason": reason,
			"error":  err.Error(),
		})
		return
	}
	diaglog.Event("mac_approval_helper_started", map[string]any{
		"path":   helper,
		"pid":    cmd.Process.Pid,
		"reason": reason,
	})
	go func() {
		err := cmd.Wait()
		exitCode := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if err != nil {
			exitCode = -1
		}
		status := macApprovalHelperStatus(exitCode)
		diaglog.Event("mac_approval_helper_finished", map[string]any{
			"path":     helper,
			"reason":   reason,
			"status":   status,
			"exitCode": exitCode,
			"stderr":   strings.TrimSpace(stderr.String()),
		})
		if status == "permission_denied" {
			// Best effort: only visible while this CLI is still waiting.
			fmt.Fprintf(os.Stderr, "obu: Chrome is waiting for remote-debugging approval, but the approval helper lacks macOS Accessibility permission.\n     Enable \"Open Browser Use Approval Helper\" in System Settings > Privacy & Security > Accessibility, or run:\n     %q setup\n", helper)
		}
	}()
}
