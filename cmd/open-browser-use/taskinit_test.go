package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		"../slug", "./slug", "www./slug", "www.../slug", " /slug",
		"a\\b/slug", "C:/slug", "__pycache__/slug", ".DS_Store/slug",
		"x.com/bad\nOBU_PROFILE=other", "bad\ndomain/slug", "x.com/\x00",
	} {
		if _, _, err := parseTaskArg(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
	for _, valid := range []string{"localhost/task", "intranet_host/task", "例子.测试/抓取", "x.com/task with spaces"} {
		if _, _, err := parseTaskArg(valid); err != nil {
			t.Errorf("flexible domain/slug rejected: %q: %v", valid, err)
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

func TestTaskWorkspaceNameUsesOnlySlugAndUniqueSuffix(t *testing.T) {
	root := t.TempDir()
	path, session, err := createTaskWorkspace(root, "example.test", "timeline-posts", taskTarget{})
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(path)
	suffix, ok := strings.CutPrefix(name, "timeline-posts-")
	if !ok || suffix == "" || strings.Contains(suffix, "-") {
		t.Fatalf("want slug plus unique suffix, without a timestamp: %q", name)
	}
	if filepath.Dir(path) != filepath.Join(root, "example.test") {
		t.Fatalf("task escaped domain directory: %s", path)
	}
	if session != "obu-example.test-"+name {
		t.Fatalf("session must retain domain and instance name: %q", session)
	}
}

func TestTaskWorkspacePreservesAndResumesLegacyTimestampDirectory(t *testing.T) {
	requirePython3(t)
	root := t.TempDir()
	legacy := filepath.Join(root, "example.test", "scan-20260827-165430")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	const session = "obu-scan-20260827-165430"
	envPath := filepath.Join(legacy, taskEnvFileName)
	original := []byte("OBU_SESSION_ID=" + session + "\nCUSTOM_SETTING=keep\n")
	if err := os.WriteFile(envPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(legacy, "01.py")
	if err := os.WriteFile(script, []byte("print(_SESSION_ID)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh, _, err := createTaskWorkspace(root, "example.test", "scan", taskTarget{})
	if err != nil || fresh == legacy {
		t.Fatalf("new instance reused legacy directory: %q %v", fresh, err)
	}
	cmd := newExecCommand()
	// Pure Python only: do not discover or connect to a real browser.
	cmd.SetArgs([]string{"--socket", filepath.Join(root, "missing.sock"), script})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("legacy exec: %v (%s)", err, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != session {
		t.Fatalf("legacy session changed: %q", stdout.String())
	}
	after, err := os.ReadFile(envPath)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatalf("legacy env was migrated or overwritten: %q %v", after, err)
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
