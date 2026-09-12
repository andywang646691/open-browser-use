package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilterTabsForDomainUsesExactNormalizedHostAndKeepsOwnership(t *testing.T) {
	raw := []any{
		map[string]any{"id": 1, "url": "https://x.com/home", "title": "Home", "ownerSessionId": nil},
		map[string]any{"id": 2, "url": "https://www.X.com/orders", "title": "Orders", "ownerSessionId": "obu-other"},
		map[string]any{"id": 3, "url": "https://app.x.com/", "title": "Subdomain", "ownerSessionId": nil},
		map[string]any{"id": 4, "url": "https://other.test/", "title": "Other", "ownerSessionId": nil},
		map[string]any{"id": 5, "url": "chrome://settings/", "title": "Settings", "ownerSessionId": nil},
	}
	matched := filterTabsForDomain(raw, "x.com")
	if len(matched) != 2 {
		t.Fatalf("matched %d tabs, want 2: %+v", len(matched), matched)
	}
	if matched[0]["id"] != 1 || matched[1]["id"] != 2 {
		t.Fatalf("unexpected matched tabs: %+v", matched)
	}
	if matched[1]["ownerSessionId"] != "obu-other" {
		t.Fatalf("ownership was not preserved: %+v", matched[1])
	}
}

func TestLoadBootstrapMemoryMatchesDomainAndLoadsOnlyLinkedArtifacts(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "techniques"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "runbooks"), 0o700); err != nil {
		t.Fatal(err)
	}
	lines := strings.Join([]string{
		"domain=x.com|kind=tip|note=use api|tech=network-fallback|runbook=x-fetch.py",
		"domain=www.X.com|kind=surprise|note=second row|unlock=wait",
		"domain=app.x.com|kind=tip|note=subdomain must not leak|tech=subdomain-only",
		"domain=other.test|kind=tip|note=other must not leak|runbook=other.py",
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(root, "site-memory.lines"), []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "techniques", "network-fallback.md"), []byte("# Network fallback\n关键词: api\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "runbooks", "x-fetch.py"), []byte("print('x')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "techniques", "subdomain-only.md"), []byte("must not load\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := loadBootstrapMemory(root, "x.com")
	if len(ctx.Rows) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(ctx.Rows), ctx.Rows)
	}
	if len(ctx.Artifacts) != 2 {
		t.Fatalf("got %d artifacts, want 2: %+v warnings=%v", len(ctx.Artifacts), ctx.Artifacts, ctx.Warnings)
	}
	got := map[string]string{}
	for _, artifact := range ctx.Artifacts {
		got[artifact.Kind+":"+artifact.Ref] = artifact.Content
	}
	if !strings.Contains(got["technique:network-fallback"], "Network fallback") {
		t.Fatalf("linked technique missing: %+v", got)
	}
	if !strings.Contains(got["runbook:x-fetch.py"], "print('x')") {
		t.Fatalf("linked runbook missing: %+v", got)
	}
	if _, exists := got["technique:subdomain-only"]; exists {
		t.Fatalf("subdomain artifact leaked into exact-domain bootstrap: %+v", got)
	}
}

func TestBootstrapMemoryRejectsUnsafeArtifactRefs(t *testing.T) {
	root := t.TempDir()
	line := "domain=x.com|kind=tip|note=bad ref|tech=../secret|runbook=../../secret.py\n"
	if err := os.WriteFile(filepath.Join(root, "site-memory.lines"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := loadBootstrapMemory(root, "x.com")
	if len(ctx.Artifacts) != 0 {
		t.Fatalf("unsafe refs must not be loaded: %+v", ctx.Artifacts)
	}
	if len(ctx.Warnings) != 2 {
		t.Fatalf("want warnings for unsafe refs, got %v", ctx.Warnings)
	}
}

func TestBootstrapLinkedArtifactIsBounded(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "techniques"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "site-memory.lines"), []byte("domain=x.com|kind=tip|note=large|tech=large\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("x", bootstrapSingleArtifactMax+100)
	if err := os.WriteFile(filepath.Join(root, "techniques", "large.md"), []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := loadBootstrapMemory(root, "x.com")
	if len(ctx.Artifacts) != 1 || !ctx.Artifacts[0].Truncated {
		t.Fatalf("large artifact should be truncated: %+v", ctx.Artifacts)
	}
	if len(ctx.Artifacts[0].Content) != bootstrapSingleArtifactMax {
		t.Fatalf("artifact content len=%d, want %d", len(ctx.Artifacts[0].Content), bootstrapSingleArtifactMax)
	}
}

func TestRenderBootstrapContextIncludesMatchedOwnership(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer
	renderBootstrapContext(&out, "x.com", []map[string]any{
		{"id": 42, "url": "https://x.com/orders", "title": "Orders\nToday", "ownerSessionId": "obu-existing"},
	}, nil, root)
	text := out.String()
	for _, want := range []string{
		"BOOTSTRAP CONTEXT",
		"matched tabs: 1 (domain=x.com)",
		"id=42 ownerSessionId=\"obu-existing\"",
		"title=\"Orders Today\"",
		"site memory: 0 row(s)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("bootstrap output missing %q:\n%s", want, text)
		}
	}
}
