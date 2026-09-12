package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskRefRoundTripAndResolution(t *testing.T) {
	root := t.TempDir()
	taskDir, _, err := createTaskWorkspace(root, "example.test", "fetch", taskTarget{})
	if err != nil {
		t.Fatal(err)
	}
	ref := taskRefForDir("example.test", taskDir)
	if !strings.HasPrefix(ref, "example.test/fetch-") {
		t.Fatalf("unexpected task ref %q", ref)
	}
	resolved, err := resolveTaskRef(root, ref)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(taskDir)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Fatalf("resolved %q, want %q", resolved, want)
	}
}

func TestTaskRefRejectsTraversalAndSymlinkWorkspace(t *testing.T) {
	root := t.TempDir()
	for _, ref := range []string{
		"../outside",
		"example.test/..",
		"example.test/a/b",
		"example.test\\evil/task",
		"/absolute",
	} {
		if _, err := resolveTaskRef(root, ref); err == nil {
			t.Errorf("expected task ref rejection for %q", ref)
		}
	}

	domainDir := filepath.Join(root, "example.test")
	if err := os.MkdirAll(domainDir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(domainDir, "linked-task")); err != nil {
		t.Skip(err)
	}
	if _, err := resolveTaskRef(root, "example.test/linked-task"); err == nil {
		t.Fatal("symlink task workspace must be rejected")
	}
}

func TestResolveTaskScriptRequiresWorkspaceFileName(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "02_fetch.py")
	if err := os.WriteFile(good, []byte("print('ok')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveTaskScript(dir, "02_fetch.py")
	if err != nil || resolved != good {
		t.Fatalf("resolved=%q err=%v, want %q", resolved, err, good)
	}
	for _, bad := range []string{"../escape.py", "sub/child.py", "/tmp/x.py", `sub\\child.py`, ".", ".."} {
		if _, err := resolveTaskScript(dir, bad); err == nil {
			t.Errorf("expected script rejection for %q", bad)
		}
	}

	outside := filepath.Join(t.TempDir(), "outside.py")
	if err := os.WriteFile(outside, []byte("print('outside')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked.py")); err != nil {
		t.Skip(err)
	}
	if _, err := resolveTaskScript(dir, "linked.py"); err == nil {
		t.Fatal("symlink task script must be rejected")
	}
}
