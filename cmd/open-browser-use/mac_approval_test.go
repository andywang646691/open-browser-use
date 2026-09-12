package main

import "testing"

func TestRequestMayNeedDirectCDPApproval(t *testing.T) {
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
