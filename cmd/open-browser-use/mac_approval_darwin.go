//go:build darwin

package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

//go:embed mac_approve.applescript
var macApproveAppleScript string

//go:embed mac_dismiss_banner.js
var macDismissBannerJXA string

func runMacRemoteDebuggingUIHelper(ctx context.Context) error {
	for ctx.Err() == nil {
		attemptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		cmd := exec.CommandContext(attemptCtx, "/usr/bin/osascript")
		cmd.Stdin = strings.NewReader(macApproveAppleScript)
		output, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("macOS remote-debugging approval helper failed: %w (%s)", err, strings.TrimSpace(string(output)))
		}
		switch strings.TrimSpace(string(output)) {
		case "ready":
			startMacRemoteDebuggingBannerHelper()
			return nil
		case "not-found":
			// The browser-root WebSocket is intentionally allowed to remain
			// pending while Chrome creates the per-session approval sheet.
		default:
			return fmt.Errorf("unexpected macOS remote-debugging approval result: %q", strings.TrimSpace(string(output)))
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
	return ctx.Err()
}

func startMacRemoteDebuggingBannerHelper() {
	executable, err := os.Executable()
	if err != nil || executable == "" {
		return
	}
	cmd := exec.Command(executable, macBannerHelperCommand)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	_ = cmd.Start()
}

func runMacRemoteDebuggingBannerHelper(ctx context.Context) error {
	for ctx.Err() == nil {
		attemptCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		dismiss := exec.CommandContext(attemptCtx, "/usr/bin/osascript", "-l", "JavaScript")
		dismiss.Stdin = strings.NewReader(macDismissBannerJXA)
		out, dismissErr := dismiss.CombinedOutput()
		cancel()
		if dismissErr == nil && strings.TrimSpace(string(out)) == "dismissed" {
			return nil
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
