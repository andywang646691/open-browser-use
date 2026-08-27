package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseTaskArgNormalizesAndValidates(t *testing.T) {
	domain, slug, err := parseTaskArg("WWW.X.COM/timeline-posts")
	if err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
	if domain != "x.com" || slug != "timeline-posts" {
		t.Fatalf("got domain=%q slug=%q, want x.com/timeline-posts", domain, slug)
	}
	// domain without slash fails
	for _, bad := range []string{
		"timeline-posts",
		"x.com/",
		"/timeline-posts",
		"x.com/a/b",
		"x.com/..",
		"x.com/.",
		"x.com/__pycache__",
		"x.com/.DS_Store",
		"x.com/a/b\\c",
	} {
		if _, _, err := parseTaskArg(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
	// www. repeated is fine, and slug length capped
	domain, _, err = parseTaskArg("www.www.x.com/slug")
	if err != nil || domain != "x.com" {
		t.Errorf("www stripping: got %q err %v", domain, err)
	}
	if _, _, err := parseTaskArg("x.com/" + strings.Repeat("a", 65)); err == nil {
		t.Error("expected error for over-long slug")
	}
}

func TestParseEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env.txt")
	content := strings.Join([]string{
		"# comment",
		"OBU_SESSION_ID=obu-x",
		" OBU_BROWSER = Google Chrome ",
		"",
		"OBU_PROFILE=Default",
		"no-equals-here",
		"MY_KEY=hello world",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	env, err := parseEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if env["OBU_SESSION_ID"] != "obu-x" || env["OBU_BROWSER"] != "Google Chrome" || env["OBU_PROFILE"] != "Default" {
		t.Fatalf("unexpected parse: %+v", env)
	}
	if env["no-equals-here"] != "" {
		t.Errorf("line without '=' should be skipped: %+v", env)
	}
	if env["MY_KEY"] != "hello world" {
		t.Errorf("custom key lost: %+v", env)
	}
}

func TestLoadTaskEnvSameDirectoryOnly(t *testing.T) {
	root := t.TempDir()
	taskDir := filepath.Join(root, "x.com", "tl")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(taskDir, "01.py")
	if err := os.WriteFile(script, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// absent → nil, nil
	env, err := loadTaskEnv(script)
	if err != nil || env != nil {
		t.Fatalf("absent env: got %v %v", env, err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, ".obu.env"), []byte("OBU_SESSION_ID=obu-tl\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env, err = loadTaskEnv(script)
	if err != nil {
		t.Fatal(err)
	}
	if env["OBU_SESSION_ID"] != "obu-tl" {
		t.Fatalf("same-dir env not loaded: %+v", env)
	}
	// a parent-level .obu.env must NOT be picked up (task-scoped, no upward search)
	parent := filepath.Join(root, "x.com")
	if err := os.WriteFile(filepath.Join(parent, ".obu.env"), []byte("OBU_SESSION_ID=obu-parent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env, err = loadTaskEnv(script)
	if err != nil {
		t.Fatal(err)
	}
	if env["OBU_SESSION_ID"] != "obu-tl" {
		t.Fatalf("upward search leaked parent env: %+v", env)
	}
}

func TestExecAutoLoadsSameDirectoryEnv(t *testing.T) {
	requirePython3(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".obu.env"),
		[]byte("OBU_SESSION_ID=obu-task-init\nOBU_BROWSER=Google Chrome\nOBU_PROFILE=Default\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "check_env.py")
	if err := os.WriteFile(script, []byte("print(_SESSION_ID)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Command layer folds file values into options (no flags set) and passes
	// the full file env; the prelude then reads the file's session id.
	var stdout bytes.Buffer
	if err := runExecFile(script,
		socketOptions{sessionID: "obu-task-init", browser: "Google Chrome", profile: "Default"},
		map[string]string{"OBU_SESSION_ID": "obu-task-init", "OBU_BROWSER": "Google Chrome", "OBU_PROFILE": "Default"},
		&stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if !strings.Contains(stdout.String(), "obu-task-init") {
		t.Fatalf("session id from .obu.env not applied: %q", stdout.String())
	}
}

func TestExecExplicitFlagBeatsEnvFile(t *testing.T) {
	requirePython3(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".obu.env"), []byte("OBU_SESSION_ID=obu-from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "check_env.py")
	if err := os.WriteFile(script, []byte("print(_SESSION_ID)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Explicit --session-id in options wins: the command layer drops the file
	// key before it reaches the subprocess, so extraEnv carries nothing here.
	var stdout bytes.Buffer
	err := runExecFile(script,
		socketOptions{sessionID: "obu-from-flag"},
		nil,
		&stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "obu-from-flag") {
		t.Fatalf("flag should win over env file, got %q", stdout.String())
	}
}

func TestExecCommandEnvFileAndFlagPriority(t *testing.T) {
	requirePython3(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".obu.env"), []byte("OBU_SESSION_ID=obu-from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "check_env.py")
	if err := os.WriteFile(script, []byte("print(_SESSION_ID)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		cmd := newExecCommand()
		cmd.SetArgs(args)
		var stdout bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&bytes.Buffer{})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("exec failed: %v", err)
		}
		return stdout.String()
	}
	if out := run(script); !strings.Contains(out, "obu-from-file") {
		t.Fatalf("no flag: .obu.env should apply, got %q", out)
	}
	if out := run("--session-id", "obu-from-flag", script); !strings.Contains(out, "obu-from-flag") {
		t.Fatalf("explicit flag should win, got %q", out)
	}
}

func TestTaskDirNameIsUniquePerInstance(t *testing.T) {
	first := taskDirName("timeline-posts", time.Date(2026, 8, 27, 16, 54, 30, 0, time.Local))
	second := taskDirName("timeline-posts", time.Date(2026, 8, 27, 16, 55, 1, 0, time.Local))
	if first == second {
		t.Fatal("two instances must not share a directory name")
	}
	if first != "timeline-posts-20260827-165430" {
		t.Errorf("unexpected stamped name: %q", first)
	}
	// lexicographic order equals time order (sortable in tasksizes)
	if !(first < second) {
		t.Errorf("stamped names must sort by time, got %q >= %q", first, second)
	}
	// decreasing timestamps sort after newer ones
	older := taskDirName("timeline-posts", time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local))
	if !(older < first) {
		t.Errorf("sort order broken: %q should be < %q", older, first)
	}
}

func TestTaskinitWritesAndReusesEnvFile(t *testing.T) {
	root := t.TempDir()
	taskDir := filepath.Join(root, "x.com", "timeline-posts")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := taskTarget{Browser: "Google Chrome", Profile: "Default"}
	written, session, err := writeTaskEnvFile(taskDir, "obu-timeline-posts", target)
	if err != nil || !written || session != "obu-timeline-posts" {
		t.Fatalf("first write: written=%v session=%q err=%v", written, session, err)
	}
	payload, err := os.ReadFile(filepath.Join(taskDir, ".obu.env"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"OBU_SESSION_ID=obu-timeline-posts", "OBU_BROWSER=Google Chrome", "OBU_PROFILE=Default"} {
		if !strings.Contains(string(payload), want) {
			t.Errorf("env file missing %q:\n%s", want, payload)
		}
	}
	// second run keeps the existing file and reuses its session id verbatim
	writtenAgain, sessionAgain, err := writeTaskEnvFile(taskDir, "obu-other", target)
	if err != nil || writtenAgain || sessionAgain != "obu-timeline-posts" {
		t.Fatalf("reuse: written=%v session=%q err=%v", writtenAgain, sessionAgain, err)
	}
	after, _ := os.ReadFile(filepath.Join(taskDir, ".obu.env"))
	if !strings.Contains(string(after), "obu-timeline-posts") || strings.Contains(string(after), "obu-other") {
		t.Errorf("existing env was overwritten:\n%s", after)
	}
}

func TestMergeEnvVars(t *testing.T) {
	base := []string{"A=1", "B=2", "C=3"}
	out := mergeEnvVars(base, map[string]string{"B": "20", "D": "4"})
	if !containsEnv(out, "B=20") || containsEnv(out, "B=2") {
		t.Errorf("override failed: %v", out)
	}
	if !containsEnv(out, "A=1") || !containsEnv(out, "C=3") || !containsEnv(out, "D=4") {
		t.Errorf("merge lost entries: %v", out)
	}
	if len(out) != 4 {
		t.Errorf("expected 4 entries, got %v", out)
	}
}

func containsEnv(env []string, want string) bool {
	for _, kv := range env {
		if kv == want {
			return true
		}
	}
	return false
}