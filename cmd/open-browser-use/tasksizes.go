package main

// obu tasksizes — audit the per-task script workspace (tasks/<domain>/<slug>/)
// grouped by domain. Read-only: reports sizes, largest per domain, and
// (with --older-than) idle candidates for cleanup; it never deletes anything.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// taskSizeEntry is one task-slug directory under a domain.
type taskSizeEntry struct {
	Name     string    // task-slug directory name
	Bytes    int64
	Files    int
	ModTime  time.Time
	IdleDays int
}

// domainSizeEntry is one first-level directory under the task root.
type domainSizeEntry struct {
	Domain  string
	Tasks   int
	Files   int
	Bytes   int64
	Largest *taskSizeEntry // task with the most bytes in this domain
}

// taskSizeScan is the audit result; Tasks is a flat listing (domain/slug)
// across all domains, used for stale-candidate reporting.
type taskSizeScan struct {
	Root       string
	Domains    []domainSizeEntry
	Tasks      []staleEntry
	TotalTasks int
	TotalFiles int
	TotalBytes int64
}

// staleEntry is one task in path form, for stale reporting and --json.
type staleEntry struct {
	Task     string `json:"task"` // "domain/slug"
	Bytes    int64  `json:"bytes"`
	Files    int    `json:"files"`
	IdleDays int    `json:"idleDays"`
}

// defaultTaskRoot returns $OBU_TASK_DIR or the default workspace
// ~/.open-browser-use/tasks, mirroring the memory store's $OBU_MEMORY_DIR
// override pattern.
func defaultTaskRoot() string {
	if value := os.Getenv("OBU_TASK_DIR"); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".open-browser-use", "tasks")
}

// isTaskIgnoredEntry reports whether a path component should be skipped while
// scanning task workspaces (python bytecode caches and macOS cruft).
func isTaskIgnoredEntry(name string, isDir bool) bool {
	if isDir {
		return name == "__pycache__"
	}
	return strings.HasSuffix(name, ".pyc") || name == ".DS_Store"
}

// scanTaskRoot walks one level of domains, then one level of task-slug dirs.
// Loose files directly under a domain count toward the domain's file/byte
// totals but not toward the task count. A missing root yields an empty scan
// (the caller prints a friendly hint), not an error.
func scanTaskRoot(root string) (*taskSizeScan, error) {
	scan := &taskSizeScan{Root: root}
	domainEntries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return scan, nil
		}
		return nil, err
	}
	for _, domainEntry := range domainEntries {
		if !domainEntry.IsDir() {
			continue
		}
		domainPath := filepath.Join(root, domainEntry.Name())
		domain := &domainSizeEntry{Domain: domainEntry.Name()}
		taskEntries, err := os.ReadDir(domainPath)
		if err != nil {
			continue
		}
		for _, taskEntry := range taskEntries {
			taskPath := filepath.Join(domainPath, taskEntry.Name())
			if taskEntry.IsDir() {
				if isTaskIgnoredEntry(taskEntry.Name(), true) {
					continue
				}
				entry := &taskSizeEntry{Name: taskEntry.Name()}
				walkTaskFiles(taskPath, entry)
				entry.IdleDays = idleDays(entry.ModTime)
				domain.Tasks++
				domain.Files += entry.Files
				domain.Bytes += entry.Bytes
				scan.Tasks = append(scan.Tasks, staleEntry{
					Task:     filepath.Join(domainEntry.Name(), taskEntry.Name()),
					Bytes:    entry.Bytes,
					Files:    entry.Files,
					IdleDays: entry.IdleDays,
				})
				if domain.Largest == nil || entry.Bytes > domain.Largest.Bytes {
					largest := *entry
					domain.Largest = &largest
				}
			} else if !isTaskIgnoredEntry(taskEntry.Name(), false) {
				info, err := os.Stat(taskPath)
				if err != nil {
					continue
				}
				domain.Files++
				domain.Bytes += info.Size()
			}
		}
		if domain.Tasks > 0 || domain.Files > 0 {
			scan.Domains = append(scan.Domains, *domain)
			scan.TotalTasks += domain.Tasks
			scan.TotalFiles += domain.Files
			scan.TotalBytes += domain.Bytes
		}
	}
	sortDomains(scan.Domains, "size")
	return scan, nil
}

// walkTaskFiles accumulates bytes/files and the latest *file* mtime under a
// task dir. Directory mtimes reflect creation/rename, not activity, so they
// are ignored for idle computation; an empty task dir falls back to its own
// mtime so it is not misreported as freshly active forever.
func walkTaskFiles(dir string, entry *taskSizeEntry) {
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == dir {
			return nil
		}
		if isTaskIgnoredEntry(d.Name(), d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		entry.Bytes += info.Size()
		entry.Files++
		if info.ModTime().After(entry.ModTime) {
			entry.ModTime = info.ModTime()
		}
		return nil
	})
	if entry.Files == 0 {
		if dirInfo, err := os.Stat(dir); err == nil {
			entry.ModTime = dirInfo.ModTime()
		}
	}
}

func idleDays(modTime time.Time) int {
	elapsed := time.Since(modTime)
	if elapsed < 0 {
		return 0
	}
	return int(elapsed.Hours() / 24)
}

func sortDomains(domains []domainSizeEntry, by string) {
	sort.SliceStable(domains, func(i, j int) bool {
		if by == "name" {
			return domains[i].Domain < domains[j].Domain
		}
		if domains[i].Bytes != domains[j].Bytes {
			return domains[i].Bytes > domains[j].Bytes
		}
		return domains[i].Domain < domains[j].Domain
	})
}

func humanBytes(bytes int64) string {
	const unit = 1024.0
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	value := float64(bytes)
	for _, suffix := range []string{"KB", "MB", "GB", "TB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f %s", value, "TB")
}

// staleCandidates lists tasks idle longer than olderThanDays, oldest first.
func staleCandidates(scan *taskSizeScan, olderThanDays int) []staleEntry {
	if olderThanDays <= 0 {
		return nil
	}
	var out []staleEntry
	for _, task := range scan.Tasks {
		if task.IdleDays > olderThanDays {
			out = append(out, task)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IdleDays != out[j].IdleDays {
			return out[i].IdleDays > out[j].IdleDays
		}
		return out[i].Task < out[j].Task
	})
	return out
}

func renderTasksizesHuman(writer io.Writer, scan *taskSizeScan, olderThanDays int) {
	fmt.Fprintf(writer, "TASK STORAGE: %s\n", scan.Root)
	if len(scan.Domains) == 0 {
		fmt.Fprintln(writer, "No task workspace yet — create tasks/<domain>/<task-slug>/ under this root.")
		return
	}
	width := 8
	for _, domain := range scan.Domains {
		if len(domain.Domain)+3 > width {
			width = len(domain.Domain) + 3
		}
	}
	headerDomain := padRight("DOMAIN", width)
	fmt.Fprintf(writer, "%s  TASKS  FILES  SIZE\n", headerDomain)
	for _, domain := range scan.Domains {
		fmt.Fprintf(writer, "%s  %5d  %5d  %7s\n",
			padRight(domain.Domain, width), domain.Tasks, domain.Files, humanBytes(domain.Bytes))
	}
	fmt.Fprintf(writer, "%s  %5d  %5d  %7s\n\n",
		padRight("TOTAL", width), scan.TotalTasks, scan.TotalFiles, humanBytes(scan.TotalBytes))
	fmt.Fprintln(writer, "LARGEST PER DOMAIN")
	for _, domain := range scan.Domains {
		if domain.Largest == nil {
			continue
		}
		fmt.Fprintf(writer, "%s/%s  %s\n",
			domain.Domain, domain.Largest.Name, humanBytes(domain.Largest.Bytes))
	}
	if olderThanDays > 0 {
		candidates := staleCandidates(scan, olderThanDays)
		fmt.Fprintf(writer, "\nIDLE > %d DAYS (dry-run, would remove)\n", olderThanDays)
		if len(candidates) == 0 {
			fmt.Fprintln(writer, "  (none)")
		}
		for _, candidate := range candidates {
			fmt.Fprintf(writer, "%s  %s  (idle %dd)\n",
				candidate.Task, humanBytes(candidate.Bytes), candidate.IdleDays)
		}
		fmt.Fprintln(writer, "This build only reports — nothing is removed.")
	}
}

func padRight(text string, width int) string {
	if len(text) >= width {
		return text
	}
	return text + strings.Repeat(" ", width-len(text))
}

func renderTasksizesJSON(writer io.Writer, scan *taskSizeScan, olderThanDays int) error {
	type largestJSON struct {
		Task  string `json:"task"`
		Bytes int64  `json:"bytes"`
		Files int    `json:"files"`
	}
	type domainJSON struct {
		Domain  string       `json:"domain"`
		Tasks   int          `json:"tasks"`
		Files   int          `json:"files"`
		Bytes   int64        `json:"bytes"`
		Largest *largestJSON `json:"largest,omitempty"`
	}
	domains := make([]domainJSON, 0, len(scan.Domains))
	for _, domain := range scan.Domains {
		entry := domainJSON{
			Domain: domain.Domain,
			Tasks:  domain.Tasks,
			Files:  domain.Files,
			Bytes:  domain.Bytes,
		}
		if domain.Largest != nil {
			entry.Largest = &largestJSON{
				Task:  domain.Largest.Name,
				Bytes: domain.Largest.Bytes,
				Files: domain.Largest.Files,
			}
		}
		domains = append(domains, entry)
	}
	payload := struct {
		Root    string       `json:"root"`
		Domains []domainJSON `json:"domains"`
		Total   struct {
			Tasks int   `json:"tasks"`
			Files int   `json:"files"`
			Bytes int64 `json:"bytes"`
		} `json:"total"`
		Stale []staleEntry `json:"stale,omitempty"`
	}{
		Root:    scan.Root,
		Domains: domains,
	}
	payload.Total.Tasks = scan.TotalTasks
	payload.Total.Files = scan.TotalFiles
	payload.Total.Bytes = scan.TotalBytes
	if olderThanDays > 0 {
		payload.Stale = staleCandidates(scan, olderThanDays)
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(writer, string(encoded))
	return err
}

func newTasksizesCommand() *cobra.Command {
	var taskRoot string
	var asJSON bool
	var sortBy string
	var olderThanDays int
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "tasksizes",
		Short: "Audit task workspace sizes grouped by domain",
		Long: `Audit the per-task script workspace (default ~/.open-browser-use/tasks,
override with OBU_TASK_DIR or --path) grouped by domain: task count,
file count, and total size per domain, plus the largest task in each
domain. With --older-than N it lists tasks idle for more than N days as
cleanup candidates.

This build is read-only: it only reports and never deletes. Scripts for a
task live under tasks/<domain>/<task-slug>/ and are executed with
"obu < file".`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if olderThanDays > 0 && !dryRun {
				return errors.New("this build only supports --older-than with --dry-run (removal is not implemented)")
			}
			if sortBy != "size" && sortBy != "name" {
				return errors.New("--sort must be \"size\" or \"name\"")
			}
			root := taskRoot
			if root == "" {
				root = defaultTaskRoot()
			}
			scan, err := scanTaskRoot(root)
			if err != nil {
				return err
			}
			sortDomains(scan.Domains, sortBy)
			if asJSON {
				return renderTasksizesJSON(cmd.OutOrStdout(), scan, olderThanDays)
			}
			renderTasksizesHuman(cmd.OutOrStdout(), scan, olderThanDays)
			return nil
		},
	}
	cmd.Flags().StringVar(&taskRoot, "path", "", "task workspace root (defaults to $OBU_TASK_DIR or ~/.open-browser-use/tasks)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit machine-readable JSON")
	cmd.Flags().StringVar(&sortBy, "sort", "size", "domain sort order: size (largest first) or name")
	cmd.Flags().IntVar(&olderThanDays, "older-than", 0, "also list tasks idle for more than N days as cleanup candidates")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "required with --older-than; this build never removes anything")
	return cmd
}