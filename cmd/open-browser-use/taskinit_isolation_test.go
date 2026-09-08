package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestTaskTargetHonorsPartialAndCompositeSelectors(t *testing.T) {
	connected := []connectedProfileInfo{
		{Browser: "chrome", BrowserName: "Google Chrome", Directory: "Default", DisplayName: "Personal"},
		{Browser: "chrome", BrowserName: "Google Chrome", Directory: "Profile 1", DisplayName: "Work"},
		{Browser: "bitbrowser", BrowserName: "BitBrowser", BrowserInstance: "instance-a", Directory: "Default"},
		{Browser: "bitbrowser", BrowserName: "BitBrowser", BrowserInstance: "instance-b", Directory: "Default"},
	}
	for i := range connected {
		info := &connected[i]
		info.Target = browserProfileTarget(info.Browser, info.BrowserInstance, info.Directory)
	}
	for _, tc := range []struct{ browser, profile, target string }{
		{"", "Work", "chrome:Profile 1"},
		{"chrome", "Personal", "chrome:Default"},
		{"bitbrowser:instance-b:Default", "", "bitbrowser:instance-b:Default"},
	} {
		got, err := selectTaskTarget(connected, tc.browser, tc.profile, "taskinit example.test/check")
		if err != nil || got.Browser != tc.target {
			t.Errorf("selectors %q/%q: got %+v err=%v", tc.browser, tc.profile, got, err)
		}
		matchCount := 0
		for _, info := range connected {
			if browserSelectorMatches(got.Browser, info) && profileSelectorMatches(got.Profile, info) {
				matchCount++
			}
		}
		if matchCount != 1 {
			t.Errorf("persisted selector must route to exactly one target, got %d", matchCount)
		}
	}
	for _, tc := range []struct{ browser, profile string }{{"", "Default"}, {"bitbrowser", "Default"}, {"", "Missing"}} {
		if _, err := selectTaskTarget(connected, tc.browser, tc.profile, "taskinit example.test/check"); err == nil {
			t.Errorf("must not silently select ambiguous/mismatched target: %+v", tc)
		}
	}
	_, err := selectTaskTarget(connected[2:], "bitbrowser", "Default", "taskinit example.test/check")
	for _, want := range []string{"instance-a", "instance-b"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("rerun command must preserve instance selector: %v", err)
		}
	}
}

func TestTaskTargetUnknownProfilePinsInstanceInsteadOfActive(t *testing.T) {
	got, err := selectTaskTarget([]connectedProfileInfo{{InstanceID: "extension-instance"}}, "", "", "taskinit example.test/check")
	if err != nil || got.Profile != "extension-instance" {
		t.Fatalf("unknown profile must retain a routing selector: %+v %v", got, err)
	}
	if _, err := selectTaskTarget([]connectedProfileInfo{{}}, "", "", "taskinit example.test/check"); err == nil {
		t.Fatal("unidentifiable target must not fall through to active registry")
	}
}

func TestTaskTargetAllowsExplicitOfflinePair(t *testing.T) {
	got, err := selectTaskTarget(nil, "chrome", "Profile 9", "taskinit example.test/check")
	if err != nil || got.Profile != "Profile 9" || got.Reachable {
		t.Fatalf("offline workspace setup should remain possible: %+v %v", got, err)
	}
}

func TestTaskinitInheritsBrowserProfileEnv(t *testing.T) {
	t.Setenv("OBU_BROWSER", "chrome-beta")
	t.Setenv("OBU_PROFILE", "Work")
	cmd := newTaskinitCommand()
	for key, want := range map[string]string{"browser": "chrome-beta", "profile": "Work"} {
		got, _ := cmd.Flags().GetString(key)
		if got != want {
			t.Errorf("%s default = %q, want %q", key, got, want)
		}
	}
}

func TestTaskWorkspaceConcurrentInstancesNeverShareDirectoryOrSession(t *testing.T) {
	root := t.TempDir()
	target := taskTarget{Browser: "chrome", Profile: "Default"}
	const count = 16
	type result struct {
		path, session string
		err           error
	}
	results := make(chan result, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			domain := "example.test"
			if i%2 == 0 {
				domain = "other.test"
			}
			path, session, err := createTaskWorkspace(root, domain, "same-task", target)
			results <- result{path, session, err}
		}(i)
	}
	wg.Wait()
	close(results)
	paths, sessions := map[string]bool{}, map[string]bool{}
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if paths[result.path] || sessions[result.session] {
			t.Fatal("task identity collision")
		}
		paths[result.path], sessions[result.session] = true, true
		if !strings.HasPrefix(filepath.Base(result.path), "same-task-") {
			t.Errorf("bad name: %s", result.path)
		}
		env, err := parseEnvFile(filepath.Join(result.path, taskEnvFileName))
		if err != nil || env["OBU_SESSION_ID"] != result.session {
			t.Fatalf("wrong env: %v %v", env, err)
		}
	}
}

func TestTaskWorkspaceRefusesSymlinkDomain(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "example.test")); err != nil {
		t.Skip(err)
	}
	_, _, err := createTaskWorkspace(root, "example.test", "task", taskTarget{})
	if err == nil {
		t.Fatal("domain symlink must not escape the task root")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("outside directory modified: %v %v", entries, err)
	}
}
