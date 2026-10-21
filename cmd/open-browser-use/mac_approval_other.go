//go:build !darwin

package main

func macApprovalHelperEnvValue() string { return "" }
func startMacApprovalHelper(string)     {}
