package main

// obu taskinit <domain>/<slug> — one command for the whole task-start ritual:
// pick a target (single connected target auto-selected; multiple enumerated
// with a ready-to-run rerun line so the user picks), create the task
// workspace under the task root, write the task's .obu.env (session id,
// browser, profile), verify the target with a ping, and print the api-sheet.
// Later `obu exec <file>` auto-loads the same-directory .obu.env.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ifuryst/open-browser-use/internal/host"
	"github.com/spf13/cobra"
)

const maxTaskSlugLen = 64

// taskDirName stamps every task instance with the local time so that repeated
// runs of the same <domain>/<slug> never collide: the directory (and its
// session id) is unique per instantiation. Format is sortable (YYYYMMDD-HHMMSS)
// and stays under the slug length cap.
func taskDirName(slug string, now time.Time) string {
	return fmt.Sprintf("%s-%s", slug, now.Format("20060102-150405"))
}

// taskTarget is a resolved browser/profile pair from a connected profile.
type taskTarget struct {
	Browser  string // selector, prefer display name ("Google Chrome")
	Profile  string // profile directory ("Default")
	Label    string // human label for listing
	Reachable bool
}

// parseTaskArg splits "domain/slug" (exactly one slash; domain itself holds
// no slash). Returns an error for shapes that would break the two-level
// tasks/<domain>/<slug>/ layout or escape it.
func parseTaskArg(arg string) (domain string, slug string, err error) {
	idx := strings.LastIndex(arg, "/")
	if idx <= 0 || idx == len(arg)-1 {
		return "", "", errors.New(`taskinit requires "<domain>/<slug>", e.g. "x.com/timeline-posts"`)
	}
	domain = arg[:idx]
	slug = arg[idx+1:]
	if strings.Contains(domain, "/") {
		return "", "", errors.New("domain must not contain '/' (layout is tasks/<domain>/<slug>/")
	}
	if err := validateTaskSlug(slug); err != nil {
		return "", "", err
	}
	return normalizeTaskDomain(domain), slug, nil
}

// normalizeTaskDomain mirrors site-memory domain normalization (drop "www.",
// lowercase) so memory rows and task workspaces group under the same key.
func normalizeTaskDomain(domain string) string {
	domain = strings.ToLower(strings.TrimSpace(domain))
	for strings.HasPrefix(domain, "www.") {
		domain = domain[len("www."):]
	}
	return domain
}

// validateTaskSlug rejects slugs that would break the single-directory level,
// escape the task root, or collide with the tasksizes exclusion rules
// (__pycache__ / .DS_Store are skipped by the scanner, so a task named that
// would be invisible to the audit).
func validateTaskSlug(slug string) error {
	if slug == "" {
		return errors.New("task slug must not be empty")
	}
	if len(slug) > maxTaskSlugLen {
		return fmt.Errorf("task slug too long (%d > %d chars)", len(slug), maxTaskSlugLen)
	}
	if strings.ContainsAny(slug, `/\`) {
		return errors.New(`task slug must not contain '/' or '\' (layout is tasks/<domain>/<slug>/)`)
	}
	if slug == "." || slug == ".." {
		return errors.New(`task slug must not be "." or ".."`)
	}
	if slug == "__pycache__" || slug == ".DS_Store" {
		return fmt.Errorf("task slug %q is reserved for the audit scanner; pick another name", slug)
	}
	return nil
}

// taskEnvFile is the task-scoped env loaded by `obu exec` (same directory
// only — never an upward search) and written by taskinit.
const taskEnvFileName = ".obu.env"

// parseEnvFile reads KEY=VALUE lines with '#' comments and blank lines
// tolerated. Unknown keys are kept (scripts may read them via os.environ).
func parseEnvFile(path string) (map[string]string, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(payload), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		value := strings.TrimSpace(line[eq+1:])
		if key == "" {
			continue
		}
		out[key] = value
	}
	return out, nil
}

// loadTaskEnv returns the .obu.env in the same directory as basePath (base
// may be a file or a directory). Returns nil, nil when absent.
func loadTaskEnv(basePath string) (map[string]string, error) {
	dir := basePath
	if info, err := os.Stat(basePath); err == nil && !info.IsDir() {
		dir = filepath.Dir(basePath)
	}
	path := filepath.Join(dir, taskEnvFileName)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return parseEnvFile(path)
}

// writeTaskEnvFile writes/re-uses the task's .obu.env in task dir. A kept
// existing file is never overwritten; its session id is returned so the same
// task directory always means the same session. With time-stamped instance
// dirs the keep path is a rare same-second collision fallback.
func writeTaskEnvFile(taskDir string, sessionID string, target taskTarget) (written bool, effectiveSession string, err error) {
	path := filepath.Join(taskDir, taskEnvFileName)
	if existing, err2 := parseEnvFile(path); err2 == nil && len(existing) > 0 {
		if current := existing["OBU_SESSION_ID"]; current != "" {
			return false, current, nil
		}
	}
	lines := []string{
		"OBU_SESSION_ID=" + sessionID,
		"OBU_BROWSER=" + target.Browser,
		"OBU_PROFILE=" + target.Profile,
	}
	payload := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		return false, "", err
	}
	return true, sessionID, nil
}

// resolveTaskTarget picks the browser/profile pair to write. Explicit
// --browser/--profile win; otherwise a single connected target is adopted and
// multiple targets produce err with every candidate's rerun command line.
func resolveTaskTarget(socketDir string, browser string, profile string, taskCmd string) (taskTarget, error) {
	if browser != "" && profile != "" {
		return taskTarget{Browser: browser, Profile: profile, Label: browser + " / " + profile}, nil
	}
	connected := probeConnectedProfiles(socketDir)
	if browser != "" {
		var matches []connectedProfileInfo
		for _, info := range connected {
			if browserSelectorMatches(browser, connectedProfileInfo{
				Browser: info.Browser, BrowserName: info.BrowserName,
				BrowserInstance: info.BrowserInstance, Directory: info.Directory,
			}) {
				matches = append(matches, info)
			}
		}
		connected = matches
	}
	if len(connected) == 0 {
		label := browser
		if label == "" {
			label = "any"
		}
		return taskTarget{}, fmt.Errorf("no connected target for browser %q — open the browser with the Open Browser Use extension, then rerun taskinit", label)
	}
	if len(connected) == 1 {
		info := connected[0]
		browserName := info.BrowserName
		if browserName == "" {
			browserName = info.Browser
		}
		return taskTarget{
			Browser:   browserName,
			Profile:   info.Directory,
			Label:     info.label(),
			Reachable: true,
		}, nil
	}
	var lines []string
	for _, info := range connected {
		browserName := info.BrowserName
		if browserName == "" {
			browserName = info.Browser
		}
		lines = append(lines, fmt.Sprintf("   obu %s --browser %q --profile %q   # %s",
			taskCmd, browserName, info.Directory, info.label()))
	}
	sort.Strings(lines)
	return taskTarget{}, fmt.Errorf("%d connected targets — ask the user which to use, then rerun with --browser/--profile:\n%s",
		len(connected), strings.Join(lines, "\n"))
}

// pingTaskTarget verifies reachability of the resolved target. Failures are
// reported but not fatal: taskinit's primary job is the workspace + env file.
func pingTaskTarget(socketDir string, target taskTarget) string {
	if _, err := invokeWithProfile("", socketDir, target.Browser, target.Profile, "ping", map[string]any{}, 10*time.Second); err != nil {
		return fmt.Sprintf("⚠ unreachable (open the browser): %v", err)
	}
	return "pong"
}

func newTaskinitCommand() *cobra.Command {
	var browser string
	var profile string
	var taskRoot string
	cmd := &cobra.Command{
		Use:   "taskinit <domain>/<slug>",
		Short: "Start a task: workspace, .obu.env, target check, api-sheet",
		Long: `Start an OBU task with one command:
- resolves the target: explicit --browser/--profile win; otherwise a single
  connected target is adopted, and multiple targets are listed with their
  ready-to-rerun command lines (never silently pick — ask the user).
- creates a fresh, time-stamped instance dir <slug>-YYYYMMDD-HHMMSS under
  the task root (default ~/.open-browser-use/tasks, override $OBU_TASK_DIR
  / --path); repeated runs of the same <domain>/<slug> never collide and
  the printed directory is the one to use.
- writes .obu.env (OBU_SESSION_ID=obu-<instance>, browser, profile).
- pings the target and prints the api-sheet, so the startup ritual fits in
  one round. Domain is normalized like site memory (www. dropped); slugs are
  validated against the audit scanner's reserved names.

To continue an old instance, exec its scripts directly (obu exec <file>
auto-loads that instance's .obu.env) — no need to taskinit again.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			domain, slug, err := parseTaskArg(args[0])
			if err != nil {
				return err
			}
			root := taskRoot
			if root == "" {
				root = defaultTaskRoot()
			}
			socketDir := host.DefaultSocketDir
			target, err := resolveTaskTarget(socketDir, browser, profile, "taskinit "+args[0])
			if err != nil {
				return err
			}
			instanceSlug := taskDirName(slug, time.Now())
			taskDir := filepath.Join(root, domain, instanceSlug)
			if err := os.MkdirAll(taskDir, 0o755); err != nil {
				return err
			}
			sessionID := "obu-" + instanceSlug
			written, effectiveSession, err := writeTaskEnvFile(taskDir, sessionID, target)
			if err != nil {
				return err
			}
			writer := cmd.OutOrStdout()
			status := "written"
			if !written {
				status = "kept (existing)"
			}
			fmt.Fprintf(writer, "✅ task: %s/%s\n", domain, instanceSlug)
			fmt.Fprintf(writer, "   dir:     %s  (.obu.env %s)\n", taskDir, status)
			fmt.Fprintf(writer, "   session: %s\n", effectiveSession)
			if target.Reachable {
				fmt.Fprintf(writer, "   target:  %s  (%s)\n", target.Label, pingTaskTarget(socketDir, target))
			} else {
				fmt.Fprintf(writer, "   target:  %s  (%s)\n", target.Label, pingTaskTarget(socketDir, target))
			}
			fmt.Fprintln(writer)
			return renderAPISheet(writer)
		},
	}
	cmd.Flags().StringVar(&browser, "browser", "", "browser selector (chrome, chrome-beta, bitbrowser, or display name)")
	cmd.Flags().StringVar(&profile, "profile", "", "Chrome profile selector (directory name like \"Default\")")
	cmd.Flags().StringVar(&taskRoot, "path", "", "task workspace root (defaults to $OBU_TASK_DIR or ~/.open-browser-use/tasks)")
	return cmd
}