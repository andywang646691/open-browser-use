package main

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/ifuryst/open-browser-use/internal/diaglog"
	"github.com/spf13/cobra"
)

const (
	macApprovalHelperCommand = "__mac-approve"
	macBannerHelperCommand   = "__mac-dismiss-banner"
)

func newMacApprovalCommand() *cobra.Command {
	return &cobra.Command{
		Use:    macApprovalHelperCommand,
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			reason := os.Getenv("OBU_MAC_APPROVAL_REASON")
			diaglog.Event("mac_approval_helper_started", map[string]any{
				"reason":    reason,
				"parentPID": os.Getenv("OBU_MAC_APPROVAL_PARENT_PID"),
			})
			ctx, cancel := context.WithTimeout(cmd.Context(), 12*time.Second)
			defer cancel()
			return runMacRemoteDebuggingUIHelper(ctx, reason)
		},
	}
}

func newMacBannerCommand() *cobra.Command {
	return &cobra.Command{
		Use:    macBannerHelperCommand,
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			reason := os.Getenv("OBU_MAC_APPROVAL_REASON")
			diaglog.Event("mac_banner_helper_started", map[string]any{
				"reason":    reason,
				"parentPID": os.Getenv("OBU_MAC_APPROVAL_PARENT_PID"),
			})
			ctx, cancel := context.WithTimeout(cmd.Context(), 6*time.Second)
			defer cancel()
			return runMacRemoteDebuggingBannerHelper(ctx)
		},
	}
}

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
