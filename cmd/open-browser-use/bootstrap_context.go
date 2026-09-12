package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	bootstrapSiteMemoryBudget   = 16 * 1024
	bootstrapArtifactBudget     = 32 * 1024
	bootstrapSingleArtifactMax  = 12 * 1024
	bootstrapTabsRequestTimeout = 10 * time.Second
)

type bootstrapArtifact struct {
	Kind      string
	Ref       string
	Path      string
	Content   string
	Truncated bool
}

type bootstrapMemoryContext struct {
	Rows          []string
	RowsTruncated bool
	Artifacts     []bootstrapArtifact
	Warnings      []string
}

func defaultMemoryRoot() string {
	if value := os.Getenv("OBU_MEMORY_DIR"); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".open-browser-use", "memory")
}

func parseMemoryFields(line string) map[string]string {
	fields := map[string]string{}
	for _, part := range strings.Split(line, "|") {
		key, value, ok := strings.Cut(part, "=")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return fields
}

func loadBootstrapMemory(memoryRoot, domain string) bootstrapMemoryContext {
	ctx := bootstrapMemoryContext{}
	payload, err := os.ReadFile(filepath.Join(memoryRoot, "site-memory.lines"))
	if err != nil {
		if !os.IsNotExist(err) {
			ctx.Warnings = append(ctx.Warnings, fmt.Sprintf("site memory unavailable: %v", err))
		}
		return ctx
	}

	techRefs := map[string]struct{}{}
	runbookRefs := map[string]struct{}{}
	usedRows := 0
	for _, rawLine := range strings.Split(string(payload), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		fields := parseMemoryFields(line)
		if normalizeTaskDomain(fields["domain"]) != domain {
			continue
		}
		if usedRows+len(line)+1 <= bootstrapSiteMemoryBudget {
			ctx.Rows = append(ctx.Rows, line)
			usedRows += len(line) + 1
		} else {
			ctx.RowsTruncated = true
		}
		if ref := strings.TrimSpace(fields["tech"]); ref != "" {
			techRefs[ref] = struct{}{}
		}
		if ref := strings.TrimSpace(fields["runbook"]); ref != "" {
			runbookRefs[ref] = struct{}{}
		}
	}

	remaining := bootstrapArtifactBudget
	for _, item := range sortedMemoryRefs(techRefs, runbookRefs) {
		path, err := safeMemoryArtifactPath(memoryRoot, item.kind, item.ref)
		if err != nil {
			ctx.Warnings = append(ctx.Warnings, err.Error())
			continue
		}
		artifact := bootstrapArtifact{Kind: item.kind, Ref: item.ref, Path: path}
		if remaining <= 0 {
			artifact.Truncated = true
			ctx.Artifacts = append(ctx.Artifacts, artifact)
			continue
		}
		limit := bootstrapSingleArtifactMax
		if remaining < limit {
			limit = remaining
		}
		content, truncated, err := readRegularFileBounded(path, limit)
		if err != nil {
			if errorsIsNotExist(err) {
				ctx.Warnings = append(ctx.Warnings, fmt.Sprintf("linked %s %q missing: %s", item.kind, item.ref, path))
			} else {
				ctx.Warnings = append(ctx.Warnings, fmt.Sprintf("linked %s %q unreadable: %v", item.kind, item.ref, err))
			}
			continue
		}
		artifact.Content = content
		artifact.Truncated = truncated
		ctx.Artifacts = append(ctx.Artifacts, artifact)
		remaining -= len(content)
	}
	return ctx
}

type memoryRef struct {
	kind string
	ref  string
}

func sortedMemoryRefs(techRefs, runbookRefs map[string]struct{}) []memoryRef {
	items := make([]memoryRef, 0, len(techRefs)+len(runbookRefs))
	for ref := range techRefs {
		items = append(items, memoryRef{kind: "technique", ref: ref})
	}
	for ref := range runbookRefs {
		items = append(items, memoryRef{kind: "runbook", ref: ref})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].kind == items[j].kind {
			return items[i].ref < items[j].ref
		}
		return items[i].kind < items[j].kind
	})
	return items
}

func safeMemoryArtifactPath(memoryRoot, kind, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" || ref == "." || ref == ".." || filepath.Base(ref) != ref || strings.Contains(ref, "\\") {
		return "", fmt.Errorf("linked %s ref %q is not a safe file name", kind, ref)
	}
	var dir, ext string
	switch kind {
	case "technique":
		dir, ext = "techniques", ".md"
	case "runbook":
		dir, ext = "runbooks", ".py"
	default:
		return "", fmt.Errorf("unknown memory artifact kind %q", kind)
	}
	gotExt := filepath.Ext(ref)
	if gotExt == "" {
		ref += ext
	} else if gotExt != ext {
		return "", fmt.Errorf("linked %s ref %q must use %s", kind, ref, ext)
	}
	return filepath.Join(memoryRoot, dir, ref), nil
}

func readRegularFileBounded(path string, limit int) (string, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("not a regular file")
	}
	fh, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer fh.Close()
	payload, err := io.ReadAll(io.LimitReader(fh, int64(limit+1)))
	if err != nil {
		return "", false, err
	}
	truncated := len(payload) > limit
	if truncated {
		payload = payload[:limit]
	}
	return string(payload), truncated, nil
}

func errorsIsNotExist(err error) bool {
	return err != nil && (os.IsNotExist(err) || err == fs.ErrNotExist)
}

func fetchMatchedTaskTabs(socketDir string, target taskTarget, sessionID, domain string) ([]map[string]any, error) {
	response, err := invokeWithProfile("", socketDir, target.Browser, target.Profile, "getUserTabs", map[string]any{
		"session_id": sessionID,
	}, bootstrapTabsRequestTimeout)
	if err != nil {
		return nil, err
	}
	if rpcErr, ok := response["error"]; ok && rpcErr != nil {
		payload, _ := json.Marshal(rpcErr)
		return nil, fmt.Errorf("getUserTabs RPC error: %s", payload)
	}
	rawTabs, ok := response["result"].([]any)
	if !ok {
		return nil, fmt.Errorf("getUserTabs returned unexpected result %T", response["result"])
	}
	return filterTabsForDomain(rawTabs, domain), nil
}

func filterTabsForDomain(rawTabs []any, domain string) []map[string]any {
	matched := make([]map[string]any, 0)
	for _, raw := range rawTabs {
		tab, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		rawURL, _ := tab["url"].(string)
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Hostname() == "" {
			continue
		}
		if normalizeTaskDomain(parsed.Hostname()) == domain {
			matched = append(matched, tab)
		}
	}
	return matched
}

func renderBootstrapContext(writer io.Writer, domain string, matchedTabs []map[string]any, tabsErr error, memoryRoot string) {
	fmt.Fprintln(writer, "BOOTSTRAP CONTEXT")
	if tabsErr != nil {
		fmt.Fprintf(writer, "matched tabs: unavailable (%v)\n", tabsErr)
	} else {
		fmt.Fprintf(writer, "matched tabs: %d (domain=%s)\n", len(matchedTabs), domain)
		for _, tab := range matchedTabs {
			fmt.Fprintf(writer, "  - id=%s ownerSessionId=%s title=%q url=%s\n",
				bootstrapScalar(tab["id"]), bootstrapScalar(tab["ownerSessionId"]),
				compactBootstrapText(bootstrapString(tab["title"])), compactBootstrapText(bootstrapString(tab["url"])))
		}
	}

	memory := loadBootstrapMemory(memoryRoot, domain)
	fmt.Fprintf(writer, "site memory: %d row(s) (source=%s)\n", len(memory.Rows), filepath.Join(memoryRoot, "site-memory.lines"))
	for _, row := range memory.Rows {
		fmt.Fprintf(writer, "  %s\n", row)
	}
	if memory.RowsTruncated {
		fmt.Fprintln(writer, "  ... site-memory output truncated by bootstrap budget")
	}
	for _, artifact := range memory.Artifacts {
		fmt.Fprintf(writer, "linked %s: %s (source=%s)\n", artifact.Kind, artifact.Ref, artifact.Path)
		if artifact.Content != "" {
			fmt.Fprint(writer, artifact.Content)
			if !strings.HasSuffix(artifact.Content, "\n") {
				fmt.Fprintln(writer)
			}
		}
		if artifact.Truncated {
			fmt.Fprintln(writer, "... linked artifact truncated by bootstrap budget")
		}
	}
	for _, warning := range memory.Warnings {
		fmt.Fprintf(writer, "warning: %s\n", warning)
	}
	fmt.Fprintln(writer)
}

func bootstrapString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func bootstrapScalar(value any) string {
	if value == nil {
		return "null"
	}
	payload, err := json.Marshal(value)
	if err == nil {
		return string(payload)
	}
	return compactBootstrapText(fmt.Sprint(value))
}

func compactBootstrapText(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.TrimSpace(value)
}
