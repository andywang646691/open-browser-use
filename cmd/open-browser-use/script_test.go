package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requirePython3(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH; skipping real-Python heredoc tests")
	}
}

func runScript(script string) (string, error) {
	var stdout bytes.Buffer
	handled, err := runPipedScript(strings.NewReader(script), socketOptions{}, &stdout, &bytes.Buffer{})
	if !handled && err == nil {
		return "", nil
	}
	return stdout.String(), err
}

func requireScriptExit(t *testing.T, err error, wantCode int) {
	t.Helper()
	specific, ok := err.(*scriptExitError)
	if !ok {
		t.Fatalf("err = %v, want scriptExitError{%d}", err, wantCode)
	}
	if specific.code != wantCode {
		t.Fatalf("exit code = %d, want %d", specific.code, wantCode)
	}
}

func TestScriptRunsRealPython(t *testing.T) {
	requirePython3(t)
	output, err := runScript("print(1 + 1)\n")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if strings.TrimSpace(output) != "2" {
		t.Fatalf("output = %q, want %q", output, "2")
	}
}

func TestScriptPythonSyntaxFullLanguage(t *testing.T) {
	requirePython3(t)
	output, err := runScript(strings.Join([]string{
		"items = [i * 2 for i in range(3) if i > 0]",
		"print(items)",
		"print(\"f-string: {}\".format(len(items)))",
		"if len(items) == 2:",
		"    print(\"branch ok\")",
	}, "\n"))
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	for _, want := range []string{"[2, 4]", "f-string: 2", "branch ok"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestScriptExitCodePropagates(t *testing.T) {
	requirePython3(t)
	_, err := runScript("import sys; sys.exit(3)")
	requireScriptExit(t, err, 3)
}

func TestScriptSyntaxErrorPassesThrough(t *testing.T) {
	requirePython3(t)
	var stderr bytes.Buffer
	_, err := runPipedScript(strings.NewReader("def broken(:\n"), socketOptions{}, &bytes.Buffer{}, &stderr)
	requireScriptExit(t, err, 1)
	if !strings.Contains(stderr.String(), "SyntaxError") {
		t.Fatalf("stderr should carry the python traceback, got: %s", stderr.String())
	}
}

func TestScriptHelperErrorDeferredUntilCall(t *testing.T) {
	requirePython3(t)
	// A pure-Python script must run even when no obu host is reachable; only
	// helper calls surface the socket error (deferred via the env).
	output, err := runScript("print(\"no helpers needed\")\n")
	if err != nil {
		t.Fatalf("pure print should not fail: %v", err)
	}
	if strings.TrimSpace(output) != "no helpers needed" {
		t.Fatalf("output = %q", output)
	}
	// Force an unreachable socket so the resolution error is deterministic
	// even on machines with a live obu host.
	var stderr bytes.Buffer
	_, err = runPipedScript(
		strings.NewReader("user_tabs()\n"),
		socketOptions{socketPath: "/nonexistent-obu-test.sock"},
		&bytes.Buffer{},
		&stderr,
	)
	requireScriptExit(t, err, 1)
	if !strings.Contains(stderr.String(), "RuntimeError") {
		t.Fatalf("stderr should mention the socket error, got: %s", stderr.String())
	}
}

func TestRunPipedScriptHandlesEmptyInput(t *testing.T) {
	handled, err := runPipedScript(strings.NewReader(""), socketOptions{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("empty stdin should not error, got %v", err)
	}
	if handled {
		t.Fatal("empty stdin should not be handled (falls back to startup status)")
	}
	handled, err = runPipedScript(strings.NewReader("   \n# only a comment\n"), socketOptions{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("comment-only stdin should not error, got %v", err)
	}
	// Comment-only input is non-empty, so it counts as a script (python3 runs
	// it and prints nothing) — it does not fall back to startup status.
	if !handled {
		t.Fatal("comment-only stdin should still be handled")
	}
}

func TestScriptEnvSessionIDReachesPrelude(t *testing.T) {
	requirePython3(t)
	t.Setenv("OBU_SESSION_ID", "env-script-session")
	var stdout bytes.Buffer
	_, err := runPipedScript(strings.NewReader("print(_SESSION_ID)\n"), socketOptions{}, &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "env-script-session" {
		t.Fatalf("session id = %q, want env value", strings.TrimSpace(stdout.String()))
	}
}

func TestScriptHelpIntrospectsHelpers(t *testing.T) {
	requirePython3(t)
	output, err := runScript(strings.Join([]string{
		"print(cdp.__doc__.splitlines()[0])",
		"print(open_tab.__doc__.splitlines()[0])",
	}, "\n"))
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if !strings.Contains(output, "Run a raw Chrome DevTools Protocol") {
		t.Fatalf("cdp docstring missing, output: %s", output)
	}
	if !strings.Contains(output, "Open a new session tab") {
		t.Fatalf("open_tab docstring missing, output: %s", output)
	}
}

func TestScriptMemoryPutAppendsLabeledLine(t *testing.T) {
	requirePython3(t)
	dir := t.TempDir()
	t.Setenv("OBU_MEMORY_DIR", dir)
	output, err := runScript(strings.Join([]string{
		`memory_put("x.com", kind="surprise",`,
		`           note="timeline is virtual-scroll",`,
		`           unlock="scrape network requests",`,
		`           tech="virtual-scroll-throttle")`,
		`print("ok")`,
	}, "\n"))
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if strings.TrimSpace(output) != "ok" {
		t.Fatalf("output = %q, want ok", output)
	}
	content, err := os.ReadFile(filepath.Join(dir, "site-memory.lines"))
	if err != nil {
		t.Fatalf("memory file not written: %v", err)
	}
	line := strings.TrimSpace(string(content))
	for _, want := range []string{
		"domain=x.com|",
		"|kind=surprise|",
		"|note=timeline is virtual-scroll",
		"|unlock=scrape network requests",
		"|tech=virtual-scroll-throttle",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("line missing %q: %s", want, line)
		}
	}
}

func TestScriptMemoryPutAppendOnlyAndNormalizesDomain(t *testing.T) {
	requirePython3(t)
	dir := t.TempDir()
	t.Setenv("OBU_MEMORY_DIR", dir)
	if _, err := runScript(`memory_put("www.GitHub.com", kind="tip", note="first")` + "\n"); err != nil {
		t.Fatalf("first put failed: %v", err)
	}
	if _, err := runScript(`memory_put("www.GitHub.com", kind="tip", note="second")` + "\n"); err != nil {
		t.Fatalf("second put failed: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "site-memory.lines"))
	if err != nil {
		t.Fatalf("memory file not written: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 appended lines, got %d: %s", len(lines), content)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "domain=github.com|") {
			t.Fatalf("domain not normalized: %s", line)
		}
	}
}

func TestScriptMemoryPutValidatesInput(t *testing.T) {
	requirePython3(t)
	dir := t.TempDir()
	t.Setenv("OBU_MEMORY_DIR", dir)
	cases := []string{
		`memory_put("x.com", kind="weird", note="n")`,       // invalid kind
		`memory_put("x.com", kind="fail", note="n")`,        // missing unlock
		`memory_put("x.com", kind="tip", note="pipe | in")`, // separator in note
		`memory_put("", kind="tip", note="n")`,              // empty domain
	}
	for _, script := range cases {
		var stderr bytes.Buffer
		_, err := runPipedScript(strings.NewReader(script+"\n"), socketOptions{}, &bytes.Buffer{}, &stderr)
		requireScriptExit(t, err, 1)
		if !strings.Contains(stderr.String(), "ValueError") {
			t.Fatalf("want ValueError for %q, stderr: %s", script, stderr.String())
		}
	}
}

func TestRootCommandPipedStdinRunsScriptMode(t *testing.T) {
	requirePython3(t)
	cmd := newRootCommand()
	cmd.SetArgs(nil)
	cmd.SetIn(strings.NewReader("print(\"smoke \" + str(6 * 7))\n"))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("root piped script failed: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "smoke 42" {
		t.Fatalf("stdout = %q, want %q", stdout.String(), "smoke 42")
	}
}

func TestRootCommandPipedStdinRespectsSessionEnv(t *testing.T) {
	requirePython3(t)
	t.Setenv("OBU_SESSION_ID", "root-script-session")
	cmd := newRootCommand()
	cmd.SetArgs(nil)
	cmd.SetIn(strings.NewReader("print(_SESSION_ID)\n"))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("root piped script failed: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "root-script-session" {
		t.Fatalf("session id = %q, want env value", strings.TrimSpace(stdout.String()))
	}
}

func TestRootCommandTTYFallbackStartupStatus(t *testing.T) {
	// Empty pipe input must fall back to the startup status rendering.
	cmd := newRootCommand()
	cmd.SetArgs(nil)
	cmd.SetIn(strings.NewReader(""))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("empty stdin root run failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "Open Browser Use") {
		t.Fatalf("expected startup status output, got: %s", stdout.String())
	}
}

func TestEnvSessionIDIsFlagDefault(t *testing.T) {
	t.Setenv("OBU_SESSION_ID", "env-session")
	cmd := newOpenTabCommand()
	if value, _ := cmd.Flags().GetString("session-id"); value != "env-session" {
		t.Errorf("session-id default = %q, want env value", value)
	}
}

func TestEnvAbsentFallsBackToCLISession(t *testing.T) {
	t.Setenv("OBU_SESSION_ID", "")
	cmd := newOpenTabCommand()
	if value, _ := cmd.Flags().GetString("session-id"); value != defaultCLISessionID {
		t.Errorf("session-id default = %q, want %q", value, defaultCLISessionID)
	}
}

func TestMCPCommandEnvSessionID(t *testing.T) {
	t.Setenv("OBU_SESSION_ID", "env-mcp-session")
	cmd := newMCPCommand()
	if value, _ := cmd.Flags().GetString("session-id"); value != "env-mcp-session" {
		t.Errorf("mcp session-id default = %q, want env value", value)
	}
	t.Setenv("OBU_SESSION_ID", "")
	cmd = newMCPCommand()
	if value, _ := cmd.Flags().GetString("session-id"); value != defaultMCPSessionID {
		t.Errorf("mcp session-id default = %q, want %q", value, defaultMCPSessionID)
	}
}
