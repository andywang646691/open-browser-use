//go:build darwin

package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/ifuryst/open-browser-use/internal/diaglog"
)

//go:embed mac_approve.applescript
var macApproveAppleScript string

//go:embed mac_dismiss_banner.applescript
var macDismissBannerAppleScript string

func runMacRemoteDebuggingUIHelper(ctx context.Context, reason string) error {
	diaglog.Event("mac_approval_helper_started", map[string]any{
		"reason":    reason,
		"parentPID": os.Getenv("OBU_MAC_APPROVAL_PARENT_PID"),
	})
	for ctx.Err() == nil {
		attemptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		cmd := exec.CommandContext(attemptCtx, "/usr/bin/osascript")
		cmd.Stdin = strings.NewReader(macApproveAppleScript)
		output, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			diaglog.Event("mac_approval_probe_failed", map[string]any{
				"reason": reason,
				"error":  err.Error(),
				"output": strings.TrimSpace(string(output)),
			})
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Accessibility permission may be granted while this helper is
			// running. Keep polling instead of failing the browser request on a
			// transient -25211 from System Events.
			if strings.Contains(string(output), "-25211") || strings.Contains(string(output), "不允许辅助访问") {
				timer := time.NewTimer(250 * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
				continue
			}
			return fmt.Errorf("macOS remote-debugging approval helper failed: %w (%s)", err, strings.TrimSpace(string(output)))
		}
		result := strings.TrimSpace(string(output))
		switch result {
		case "ready":
			diaglog.Event("mac_approval_sheet_clicked", map[string]any{
				"reason": reason,
			})
			startMacRemoteDebuggingBannerHelper(reason)
			return nil
		case "not-found":
			// The browser-root WebSocket is intentionally allowed to remain
			// pending while Chrome creates the per-session approval sheet.
		default:
			diaglog.Event("mac_approval_unexpected_result", map[string]any{
				"reason": reason,
				"result": result,
			})
			return fmt.Errorf("unexpected macOS remote-debugging approval result: %q", result)
		}

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

func startMacRemoteDebuggingBannerHelper(reason string) {
	if reason == "" {
		reason = "remote_debugging_approval_clicked"
	}
	diaglog.Event("mac_banner_helper_requested", map[string]any{
		"reason": reason,
	})
	executable, err := os.Executable()
	if err != nil || executable == "" {
		return
	}
	cmd := exec.Command(executable, macBannerHelperCommand)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Env = append(os.Environ(),
		"OBU_MAC_APPROVAL_REASON="+reason,
		"OBU_MAC_APPROVAL_PARENT_PID="+strconv.Itoa(os.Getpid()),
	)
	if err := cmd.Start(); err != nil {
		diaglog.Event("mac_banner_helper_start_failed", map[string]any{
			"error": err.Error(),
		})
		return
	}
	diaglog.Event("mac_banner_helper_started", map[string]any{
		"pid": cmd.Process.Pid,
	})
}

func runMacRemoteDebuggingBannerHelper(ctx context.Context) error {
	for ctx.Err() == nil {
		attemptCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		dismiss := exec.CommandContext(attemptCtx, "/usr/bin/osascript")
		dismiss.Stdin = strings.NewReader(macDismissBannerAppleScript)
		out, dismissErr := dismiss.CombinedOutput()
		cancel()
		if dismissErr == nil && strings.TrimSpace(string(out)) == "dismissed" {
			diaglog.Event("mac_banner_dismissed", map[string]any{
				"reason": os.Getenv("OBU_MAC_APPROVAL_REASON"),
			})
			return nil
		}
		if dismissErr != nil {
			diaglog.Event("mac_banner_probe_failed", map[string]any{
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
