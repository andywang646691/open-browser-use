//go:build !darwin

package main

import "context"

func macApprovalHelperEnvValue() string                           { return "" }
func runMacRemoteDebuggingUIHelper(context.Context, string) error { return nil }
func runMacRemoteDebuggingBannerHelper(context.Context) error     { return nil }
