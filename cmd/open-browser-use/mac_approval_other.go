//go:build !darwin

package main

import "context"

func runMacRemoteDebuggingUIHelper(context.Context, string) error { return nil }
func runMacRemoteDebuggingBannerHelper(context.Context) error     { return nil }
