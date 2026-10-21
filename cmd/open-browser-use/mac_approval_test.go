package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMacApprovalHelperBuildsAndPassesSelfTest(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-only helper")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	binary := filepath.Join(t.TempDir(), "open-browser-use-mac-approval-helper")
	build := exec.Command("clang", "-O0", "-fobjc-arc", "-Wall", "-Wextra", "-Werror", "-Wno-unused-parameter",
		"-framework", "Cocoa", "-framework", "ApplicationServices",
		"-o", binary, "../../scripts/macos/open-browser-use-mac-approval-helper.m")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("approval helper should compile without warnings: %v (%s)", err, strings.TrimSpace(string(output)))
	}
	if output, err := exec.Command(binary, "self-test").CombinedOutput(); err != nil {
		t.Fatalf("approval helper self-test failed: %v (%s)", err, strings.TrimSpace(string(output)))
	}
	usage := exec.Command(binary, "no-such-operation")
	err := usage.Run()
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 2 {
		t.Fatalf("unknown operation should exit 2, got %v", err)
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

	t.Run("extension lifecycle always may need approval", func(t *testing.T) {
		if !requestMayNeedDirectCDPApproval("listExtensions", map[string]any{"_cdpMode": "extension"}) {
			t.Fatal("extension lifecycle uses the browser-root Extensions domain")
		}
	})

	t.Run("non CDP request", func(t *testing.T) {
		if requestMayNeedDirectCDPApproval("getTabs", map[string]any{"_cdpMode": "direct"}) {
			t.Fatal("getTabs must not start an approval helper")
		}
	})
}

func stubMacApprovalHelper(t *testing.T) *atomic.Int32 {
	t.Helper()
	var started atomic.Int32
	original := startMacApprovalHelperFunc
	startMacApprovalHelperFunc = func(string) { started.Add(1) }
	t.Cleanup(func() { startMacApprovalHelperFunc = original })
	return &started
}

func TestScheduleMacApprovalHelperSkipsAnsweredRequests(t *testing.T) {
	started := stubMacApprovalHelper(t)
	stop := scheduleMacApprovalHelper("fast response")
	stop()
	time.Sleep(macApprovalStartDelay + 200*time.Millisecond)
	if got := started.Load(); got != 0 {
		t.Fatalf("an answered request must not start the helper, started %d times", got)
	}
}

func TestScheduleMacApprovalHelperStartsForParkedRequests(t *testing.T) {
	started := stubMacApprovalHelper(t)
	stop := scheduleMacApprovalHelper("parked behind sheet")
	defer stop()
	deadline := time.Now().Add(macApprovalStartDelay + 2*time.Second)
	for started.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := started.Load(); got != 1 {
		t.Fatalf("a request parked behind the approval sheet should start the helper once, started %d times", got)
	}
}

func TestMacApprovalHelperStatus(t *testing.T) {
	cases := map[int]string{
		0: "approved", 1: "sheet_not_found", 2: "usage_error", 3: "permission_denied",
		4: "already_running", 5: "caller_exited", 137: "failed", -1: "failed",
	}
	for code, want := range cases {
		if got := macApprovalHelperStatus(code); got != want {
			t.Errorf("exit %d: got %q, want %q", code, got, want)
		}
	}
}
