package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildTestTaskRoot creates a sample workspace:
//
//	root/x.com/timeline-scrape/fetch.py run.log
//	root/x.com/profile-walk/01_probe.py __pycache__/x.cpython-311.pyc
//	root/github.com/issue-triage/script.py
//	root/sketchy.txt            (loose file, counts toward "github.com"? no —
//	                             loose files under the root are ignored)
func buildTestTaskRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel string, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("x.com/timeline-scrape/fetch.py", strings.Repeat("x", 2048))
	write("x.com/timeline-scrape/run.log", strings.Repeat("y", 1024))
	write("x.com/profile-walk/01_probe.py", strings.Repeat("z", 512))
	write("x.com/profile-walk/__pycache__/probe.cpython-311.pyc", strings.Repeat("p", 4096)) // ignored
	write("github.com/issue-triage/script.py", strings.Repeat("q", 256))
	write("noise.txt", "did-not-finish") // loose file at the root: ignored
	return root
}

func TestScanTaskRootGrouping(t *testing.T) {
	root := buildTestTaskRoot(t)
	scan, err := scanTaskRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Domains) != 2 {
		t.Fatalf("expected 2 domains, got %d: %+v", len(scan.Domains), scan.Domains)
	}
	xcom := scan.Domains[0] // sorted by size desc; x.com is bigger
	if xcom.Domain != "x.com" {
		t.Fatalf("expected x.com first (largest), got %s", xcom.Domain)
	}
	if xcom.Tasks != 2 {
		t.Errorf("x.com: expected 2 tasks, got %d", xcom.Tasks)
	}
	// timeline-scrape = fetch.py (2048) + run.log (1024) = 3072
	// profile-walk   = 01_probe.py (512) +pycache excluded
	if xcom.Files != 3 {
		t.Errorf("x.com: expected 3 files (pycache excluded), got %d", xcom.Files)
	}
	if xcom.Bytes != 2048+1024+512 {
		t.Errorf("x.com: expected %d bytes, got %d", 2048+1024+512, xcom.Bytes)
	}
	if xcom.Largest == nil || xcom.Largest.Name != "timeline-scrape" {
		t.Errorf("x.com largest: expected timeline-scrape, got %+v", xcom.Largest)
	}
	gh := scan.Domains[1]
	if gh.Domain != "github.com" || gh.Tasks != 1 || gh.Bytes != 256 {
		t.Errorf("github.com: unexpected stat %+v", gh)
	}
	if scan.TotalTasks != 3 || scan.TotalFiles != 4 || scan.TotalBytes != 2048+1024+512+256 {
		t.Errorf("totals wrong: %+v", scan)
	}
	// pycache dir itself was excluded from reading too
	if _, err := os.Stat(filepath.Join(root, "x.com/profile-walk/__pycache__")); err != nil {
		t.Errorf("test fixture pycache was removed? %v", err)
	}
}

func TestScanTaskRootMissing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")
	scan, err := scanTaskRoot(root)
	if err != nil {
		t.Fatalf("missing root should not error, got %v", err)
	}
	if len(scan.Domains) != 0 || scan.TotalTasks != 0 {
		t.Fatalf("expected empty scan, got %+v", scan)
	}
}

func TestHumanOutputHasAllBlocks(t *testing.T) {
	root := buildTestTaskRoot(t)
	scan, err := scanTaskRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	renderTasksizesHuman(&buf, scan, 0)
	out := buf.String()
	for _, want := range []string{"TASK STORAGE:", "DOMAIN", "x.com", "github.com", "TOTAL", "LARGEST PER DOMAIN", "timeline-scrape"} {
		if !strings.Contains(out, want) {
			t.Errorf("human output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "would remove") {
		t.Errorf("human output should not show stale block without --older-than:\n%s", out)
	}
}

func TestStaleCandidatesOnlyWhenRequested(t *testing.T) {
	root := buildTestTaskRoot(t)
	// make one task clearly old by aging only its file: directory mtime stays
	// fresh (as after a rename), which must NOT reset the idle clock.
	oldDir := filepath.Join(root, "github.com", "issue-triage")
	oldTime := time.Now().Add(-200 * 24 * time.Hour)
	fileToAge := filepath.Join(oldDir, "script.py")
	if err := os.Chtimes(fileToAge, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	scan, err := scanTaskRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := staleCandidates(scan, 90); len(got) != 1 || got[0].Task != "github.com/issue-triage" {
		t.Fatalf("expected 1 stale candidate (file-aged task), got %+v", got)
	}
	if got := staleCandidates(scan, 0); got != nil {
		t.Fatalf("expected nil stale without threshold, got %+v", got)
	}
	// dry-run human output marks intent and never claims deletion
	var buf bytes.Buffer
	renderTasksizesHuman(&buf, scan, 90)
	out := buf.String()
	if !strings.Contains(out, "would remove") || !strings.Contains(out, "issue-triage") {
		t.Errorf("dry-run block missing:\n%s", out)
	}
	if strings.Contains(out, "removed ") && !strings.Contains(out, "would remove") {
		t.Errorf("dry-run block must not claim actual removal:\n%s", out)
	}
}

func TestJSONOutputStructure(t *testing.T) {
	root := buildTestTaskRoot(t)
	scan, err := scanTaskRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := renderTasksizesJSON(&buf, scan, 90); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Root    string `json:"root"`
		Domains []struct {
			Domain  string `json:"domain"`
			Tasks   int    `json:"tasks"`
			Bytes   int64  `json:"bytes"`
			Largest struct {
				Task string `json:"task"`
			} `json:"largest"`
		} `json:"domains"`
		Total struct {
			Tasks int   `json:"tasks"`
			Bytes int64 `json:"bytes"`
		} `json:"total"`
		Stale []struct {
			Task     string `json:"task"`
			IdleDays int    `json:"idleDays"`
		} `json:"stale"`
	}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("bad json: %v\n%s", err, buf.String())
	}
	if payload.Root != root {
		t.Errorf("root mismatch: %s", payload.Root)
	}
	if len(payload.Domains) != 2 || payload.Total.Tasks != 3 {
		t.Errorf("domains/total wrong: %+v", payload)
	}
	if payload.Domains[0].Largest.Task != "timeline-scrape" {
		t.Errorf("largest wrong: %+v", payload.Domains[0])
	}
	// JSON without --older-than has no stale key
	var buf2 bytes.Buffer
	if err := renderTasksizesJSON(&buf2, scan, 0); err != nil {
		t.Fatal(err)
	}
	var payload2 map[string]any
	if err := json.Unmarshal(buf2.Bytes(), &payload2); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload2["stale"]; ok {
		t.Errorf("stale key should be omitted without --older-than: %s", buf2.String())
	}
}

func TestSortDomainsByName(t *testing.T) {
	root := buildTestTaskRoot(t)
	scan, err := scanTaskRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	sortDomains(scan.Domains, "name")
	if scan.Domains[0].Domain != "github.com" || scan.Domains[1].Domain != "x.com" {
		t.Errorf("name sort wrong: %+v", scan.Domains)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}