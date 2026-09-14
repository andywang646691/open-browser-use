//go:build darwin

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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

func macApprovalHelperPath() (string, error) {
	path := macApprovalHelperEnvValue()
	if path == "" {
		return "", fmt.Errorf("cannot determine macOS approval helper path")
	}
	return path, nil
}

func runMacRemoteDebuggingUIHelper(ctx context.Context, reason string) error {
	helper, err := macApprovalHelperPath()
	if err != nil {
		return fmt.Errorf("resolve macOS approval helper: %w", err)
	}
	diaglog.Event("mac_approval_helper_resolved", map[string]any{
		"path":   helper,
		"reason": reason,
	})
	diaglog.Event("mac_approval_helper_started", map[string]any{
		"path":      helper,
		"reason":    reason,
		"parentPID": os.Getenv("OBU_MAC_APPROVAL_PARENT_PID"),
	})

	for ctx.Err() == nil {
		attemptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		cmd := exec.CommandContext(attemptCtx, helper, "approve")
		output, err := cmd.CombinedOutput()
		cancel()
		if err == nil {
			diaglog.Event("mac_approval_sheet_clicked", map[string]any{
				"path":   helper,
				"reason": reason,
			})
			startMacRemoteDebuggingBannerHelper(reason)
			return nil
		}
		if isApprovalHelperPermissionDenied(err) {
			diaglog.Event("mac_approval_permission_denied", map[string]any{
				"path":   helper,
				"reason": reason,
				"error":  strings.TrimSpace(string(output)),
			})
			return fmt.Errorf("macOS Accessibility permission is required for %s; the helper opened System Settings and highlighted itself", helper)
		}
		if !isApprovalHelperNotFound(err) {
			diaglog.Event("mac_approval_probe_failed", map[string]any{
				"path":   helper,
				"reason": reason,
				"error":  err.Error(),
				"output": strings.TrimSpace(string(output)),
			})
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("macOS approval helper failed: %w (%s)", err, strings.TrimSpace(string(output)))
		}

		// Exit status 1 means that the helper is healthy but the sheet has not
		// appeared yet. Keep the one patient Browser Root handshake alive.
		timer := time.NewTimer(120 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		diaglog.Event("mac_approval_helper_finished", map[string]any{
			"reason": reason,
			"status": "timeout_or_cancelled",
			"error":  err.Error(),
		})
		return err
	}
	return nil
}

func isApprovalHelperPermissionDenied(err error) bool {
	if err == nil {
		return false
	}
	exitErr, ok := err.(*exec.ExitError)
	return ok && exitErr.ExitCode() == 3
}

func isApprovalHelperNotFound(err error) bool {
	if err == nil {
		return false
	}
	exitErr, ok := err.(*exec.ExitError)
	return ok && exitErr.ExitCode() == 1
}

func startMacRemoteDebuggingBannerHelper(reason string) {
	if reason == "" {
		reason = "remote_debugging_approval_clicked"
	}
	helper, err := macApprovalHelperPath()
	if err != nil || helper == "" {
		return
	}
	diaglog.Event("mac_banner_helper_requested", map[string]any{
		"path":   helper,
		"reason": reason,
	})
	cmd := exec.Command(helper, "dismiss-banner")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Env = append(os.Environ(),
		"OBU_MAC_APPROVAL_REASON="+reason,
		"OBU_MAC_APPROVAL_PARENT_PID="+strconv.Itoa(os.Getpid()),
	)
	if err := cmd.Start(); err != nil {
		diaglog.Event("mac_banner_helper_start_failed", map[string]any{
			"path":  helper,
			"error": err.Error(),
		})
		return
	}
	diaglog.Event("mac_banner_helper_started", map[string]any{
		"path": helper,
		"pid":  cmd.Process.Pid,
	})
}

func runMacRemoteDebuggingBannerHelper(ctx context.Context) error {
	helper, err := macApprovalHelperPath()
	if err != nil {
		return err
	}
	for ctx.Err() == nil {
		attemptCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		dismiss := exec.CommandContext(attemptCtx, helper, "dismiss-banner")
		out, dismissErr := dismiss.CombinedOutput()
		cancel()
		if dismissErr == nil {
			diaglog.Event("mac_banner_dismissed", map[string]any{
				"reason": os.Getenv("OBU_MAC_APPROVAL_REASON"),
			})
			return nil
		}
		if !isApprovalHelperNotFound(dismissErr) {
			diaglog.Event("mac_banner_probe_failed", map[string]any{
				"path":   helper,
				"error":  dismissErr.Error(),
				"output": strings.TrimSpace(string(out)),
			})
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		timer := time.NewTimer(150 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	return ctx.Err()
}
