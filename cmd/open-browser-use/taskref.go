package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func taskRefForDir(domain, taskDir string) string {
	return domain + "/" + filepath.Base(taskDir)
}

func parseTaskRef(ref string) (domain, instance string, err error) {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.Count(ref, "/") != 1 {
		return "", "", fmt.Errorf("task ref must be <domain>/<instance>, got %q", ref)
	}
	domain, instance, _ = strings.Cut(ref, "/")
	domain = normalizeTaskDomain(domain)
	if err := validateTaskComponent(domain, "task ref domain"); err != nil {
		return "", "", err
	}
	if err := validateTaskComponent(instance, "task ref instance"); err != nil {
		return "", "", err
	}
	return domain, instance, nil
}

func resolveTaskRef(root, ref string) (string, error) {
	domain, instance, err := parseTaskRef(ref)
	if err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	taskDir := filepath.Join(rootAbs, domain, instance)
	info, err := os.Lstat(taskDir)
	if err != nil {
		return "", fmt.Errorf("task ref %q: %w", ref, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("task ref %q does not resolve to a real task directory", ref)
	}

	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", fmt.Errorf("task root: %w", err)
	}
	taskReal, err := filepath.EvalSymlinks(taskDir)
	if err != nil {
		return "", fmt.Errorf("task ref %q: %w", ref, err)
	}
	rel, err := filepath.Rel(rootReal, taskReal)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("task ref %q escapes task root", ref)
	}
	return taskReal, nil
}

func resolveTaskScript(taskDir, script string) (string, error) {
	script = strings.TrimSpace(script)
	if script == "" {
		return "", fmt.Errorf("exec --task requires a script file name")
	}
	if filepath.IsAbs(script) || filepath.Base(script) != script || strings.Contains(script, "\\") || script == "." || script == ".." {
		return "", fmt.Errorf("exec --task script must be a file name inside the task workspace, got %q", script)
	}
	path := filepath.Join(taskDir, script)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("exec --task script must be a regular non-symlink file: %s", path)
	}
	return path, nil
}
