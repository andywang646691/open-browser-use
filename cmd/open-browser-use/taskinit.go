package main

// obu taskinit <domain>/<slug> — one command for the whole task-start ritual:
// pick a target (single connected target auto-selected; multiple enumerated
// with a ready-to-run rerun line so the user picks), create the task
// workspace under the task root, write the task's .obu.env (session id,
// browser, profile), verify the target, hydrate execution context (domain-
// matched tabs with ownership plus matching memory and linked artifacts), and
// print the api-sheet. It also prints a taskRef plus absolute workspace path;
// later `obu exec --task <taskRef> <file>` resolves the workspace independent
// of the caller cwd and auto-loads that task's .obu.env.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/ifuryst/open-browser-use/internal/host"
	"github.com/spf13/cobra"
)

const maxTaskSlugLen = 64

// taskTarget is a resolved browser/profile pair from a connected profile.
type taskTarget struct {
	Browser   string // composite target when available (preserves browser instance)
	Profile   string // profile directory ("Default")
	Label     string // human label for listing
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
		return "", "", errors.New("domain must not contain '/' (layout is tasks/<domain>/<slug>/)")
	}
	if err := validateTaskSlug(slug); err != nil {
		return "", "", err
	}
	domain = normalizeTaskDomain(domain)
	if err := validateTaskComponent(domain, "domain"); err != nil {
		return "", "", err
	}
	return domain, slug, nil
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
	if len(slug) > maxTaskSlugLen {
		return fmt.Errorf("task slug too long (%d > %d bytes)", len(slug), maxTaskSlugLen)
	}
	return validateTaskComponent(slug, "task slug")
}

// This is a path/env boundary, not a public DNS allowlist. Keep localhost,
// internal hosts, Unicode domains and human-readable slugs usable.
func validateTaskComponent(value, label string) error {
	if strings.TrimSpace(value) == "" || value == "." || value == ".." ||
		strings.ContainsAny(value, `/\:`) || strings.ContainsFunc(value, unicode.IsControl) {
		return fmt.Errorf("%s must be a non-empty directory name without path separators, ':' or control characters", label)
	}
	if strings.EqualFold(value, "__pycache__") || strings.EqualFold(value, ".DS_Store") {
		return fmt.Errorf("%s %q is reserved for the audit scanner; pick another name", label, value)
	}
	return nil
}

func createTaskWorkspace(root, domain, slug string, target taskTarget) (string, string, error) {
	if err := validateTaskComponent(domain, "domain"); err != nil {
		return "", "", err
	}
	if err := validateTaskSlug(slug); err != nil {
		return "", "", err
	}
	domainDir := filepath.Join(root, domain)
	if err := os.MkdirAll(domainDir, 0o700); err != nil {
		return "", "", err
	}
	info, err := os.Lstat(domainDir)
	if err != nil {
		return "", "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", errors.New("task domain must be a real directory, not a symlink")
	}
	taskDir, err := os.MkdirTemp(domainDir, slug+"-")
	if err != nil {
		return "", "", err
	}
	// Include the domain as well as the allocated instance name: tasks at
	// different sites must not share the browser cleanup/session namespace.
	sessionID := "obu-" + domain + "-" + filepath.Base(taskDir)
	_, sessionID, err = writeTaskEnvFile(taskDir, sessionID, target)
	if err != nil {
		_ = os.RemoveAll(taskDir) // only the fresh directory owned by this call
		return "", "", err
	}
	return taskDir, sessionID, nil
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
// task directory always means the same session. New taskinit instances are
// allocated atomically and never enter the reuse path.
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
	return selectTaskTarget(probeConnectedProfiles(socketDir), browser, profile, taskCmd)
}

func selectTaskTarget(connected []connectedProfileInfo, browser, profile, taskCmd string) (taskTarget, error) {
	if strings.ContainsFunc(browser+profile, unicode.IsControl) {
		return taskTarget{}, errors.New("browser/profile selectors must not contain control characters")
	}
	var matches []connectedProfileInfo
	for _, info := range connected {
		if browserSelectorMatches(browser, info) && profileSelectorMatches(profile, info) {
			matches = append(matches, info)
		}
	}
	if len(matches) == 0 {
		// Preserve explicit offline workspace setup, but never substitute a
		// different live target for a partial or mismatched selector.
		if browser != "" && profile != "" {
			return taskTarget{Browser: browser, Profile: profile, Label: browser + " / " + profile}, nil
		}
		return taskTarget{}, fmt.Errorf("no connected target for browser %q profile %q — open the browser with the Open Browser Use extension, then rerun taskinit", browser, profile)
	}
	if len(matches) == 1 {
		return taskTargetFromProfile(matches[0])
	}
	var lines []string
	for _, info := range matches {
		target, err := taskTargetFromProfile(info)
		if err != nil {
			return taskTarget{}, err
		}
		lines = append(lines, fmt.Sprintf("   obu %s --browser %s --profile %s   # %s",
			taskCmd, quoteTaskArg(target.Browser), quoteTaskArg(target.Profile), info.label()))
	}
	sort.Strings(lines)
	return taskTarget{}, fmt.Errorf("%d connected targets — ask the user which to use, then rerun with --browser/--profile:\n%s",
		len(matches), strings.Join(lines, "\n"))
}

func taskTargetFromProfile(info connectedProfileInfo) (taskTarget, error) {
	browser := info.Target // composite selector preserves BitBrowser instance
	if browser == "" {
		browser = info.BrowserInstance
	}
	if browser == "" {
		browser = info.BrowserName
	}
	if browser == "" {
		browser = info.Browser
	}
	profile := info.Directory
	if profile == "" {
		profile = info.InstanceID // unknown profile still has an exact live target
	}
	if profile == "" || strings.ContainsFunc(browser+profile, unicode.IsControl) {
		return taskTarget{}, errors.New("connected target has no usable profile identity; specify --browser/--profile explicitly")
	}
	return taskTarget{Browser: browser, Profile: profile, Label: info.label(), Reachable: true}, nil
}

func quoteTaskArg(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
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
		Short: "Start a task and hydrate its browser/memory context",
		Long: `Start an OBU task with one command:
- resolves the target: explicit --browser/--profile win; otherwise a single
  connected target is adopted, and multiple targets are listed with their
  ready-to-rerun command lines (never silently pick — ask the user).
- creates a fresh instance dir <slug>-<unique> under
  the task root (default ~/.open-browser-use/tasks, override $OBU_TASK_DIR
  / --path); repeated runs of the same <domain>/<slug> never collide and
  the printed directory is the one to use.
- prints a stable taskRef (<domain>/<instance>) plus the absolute task dir;
  later "obu exec --task <taskRef> <file.py>" can resolve that workspace from
  any caller cwd. A non-default root can be supplied to exec with --task-root.
- writes .obu.env (OBU_SESSION_ID=obu-<domain>-<instance>, browser, profile).
- checks the target, lists only user tabs whose normalized host matches the
  task domain (including ownerSessionId), loads matching site-memory rows plus
  explicitly linked techniques/runbooks within bounded output budgets, and
  prints the api-sheet. It does not claim tabs or execute runbooks. Domain is
  normalized like site memory (www. dropped); slugs are validated against the
  audit scanner's reserved names.

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
			root, err = filepath.Abs(root)
			if err != nil {
				return err
			}
			socketDir := host.DefaultSocketDir
			target, err := resolveTaskTarget(socketDir, browser, profile, "taskinit "+quoteTaskArg(args[0]))
			if err != nil {
				return err
			}
			taskDir, sessionID, err := createTaskWorkspace(root, domain, slug, target)
			if err != nil {
				return err
			}
			matchedTabs, tabsErr := fetchMatchedTaskTabs(socketDir, target, sessionID, domain)
			taskRef := taskRefForDir(domain, taskDir)
			writer := cmd.OutOrStdout()
			fmt.Fprintf(writer, "✅ task: %s\n", taskRef)
			fmt.Fprintf(writer, "   taskRef: %s\n", taskRef)
			fmt.Fprintf(writer, "   taskRoot: %s\n", root)
			fmt.Fprintf(writer, "   dir:     %s  (.obu.env written)\n", taskDir)
			fmt.Fprintf(writer, "   session: %s\n", sessionID)
			fmt.Fprintf(writer, "   target:  %s  (%s)\n", target.Label, pingTaskTarget(socketDir, target))
			fmt.Fprintln(writer)
			renderBootstrapContext(writer, domain, matchedTabs, tabsErr, defaultMemoryRoot())
			return renderAPISheet(writer)
		},
	}
	cmd.Flags().StringVar(&browser, "browser", os.Getenv("OBU_BROWSER"), "browser selector (chrome, chrome-beta, bitbrowser, or display name)")
	cmd.Flags().StringVar(&profile, "profile", os.Getenv("OBU_PROFILE"), "Chrome profile selector (directory name like \"Default\")")
	cmd.Flags().StringVar(&taskRoot, "path", "", "task workspace root (defaults to $OBU_TASK_DIR or ~/.open-browser-use/tasks)")
	return cmd
}
