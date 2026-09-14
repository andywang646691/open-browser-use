package main

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestMacApprovalAppleScript(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-only AppleScript")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	cmd := exec.Command("clang", "-fsyntax-only", "-fobjc-arc", "-framework", "Cocoa", "-framework", "ApplicationServices", "../../scripts/macos/open-browser-use-mac-approval-helper.m")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("approval helper should compile: %v (%s)", err, strings.TrimSpace(string(output)))
	}
}

func TestRequestMayNeedDirectCDPApproval(t *testing.T) {
	t.Run("defaults to extension without mode", func(t *testing.T) {
		t.Setenv("OBU_CDP_MODE", "")
		if requestMayNeedDirectCDPApproval("attach", map[string]any{}) {
			t.Fatal("unset CDP mode should use extension without starting the approval helper")
		}
	})

	t.Run("direct attach", func(t *testing.T) {
		if !requestMayNeedDirectCDPApproval("attach", map[string]any{"_cdpMode": "direct"}) {
			t.Fatal("direct attach should start the macOS approval helper")
		}
	})

	t.Run("auto browser command", func(t *testing.T) {
		if !requestMayNeedDirectCDPApproval("executeCdp", map[string]any{"_cdpMode": "auto"}) {
			t.Fatal("auto executeCdp may initialize direct CDP")
		}
	})

	t.Run("extension never starts helper", func(t *testing.T) {
		if requestMayNeedDirectCDPApproval("attach", map[string]any{"_cdpMode": "extension"}) {
			t.Fatal("extension mode must not start the direct-CDP approval helper")
		}
	})

	t.Run("non CDP request", func(t *testing.T) {
		if requestMayNeedDirectCDPApproval("getTabs", map[string]any{"_cdpMode": "direct"}) {
			t.Fatal("getTabs must not start an approval helper")
		}
	})
}
