---
name: open-browser-use
description: "Use Open Browser Use (OBU) for browser automation, web scraping, site testing, or any task that reads or changes a web page in the user's Chrome."
---

# Open Browser Use

Use OBU's real Chrome session through CDP: `cdp("Domain.method", **params)`,
`evaluate(js)`, and the tab helpers below.

## Process

### 1. Initialize

For a new task, run one setup command:

```sh
obu taskinit <domain>/<slug> [--browser X --profile Y]
```

This creates the task workspace, selects the browser/profile, and prints the
runtime API sheet. Use the printed `<domain>/<slug>-<unique>/` directory;
never construct its suffix yourself. If multiple targets are connected, stop and ask the user
which target to use; never silently choose one. To continue an existing task,
run `obu exec <file>` from its existing task directory instead of starting a
new `taskinit` instance.

**Done when:** the task directory and the user-selected browser target are
known.

### 2. Load context

Before the first browser action for each normalized domain in the current OBU
task:

1. Read the domain rows from
   `~/.open-browser-use/memory/site-memory.lines` (or the same file below
   `$OBU_MEMORY_DIR`).
2. Read every linked
   `~/.open-browser-use/memory/techniques/<name>.md` and
   `~/.open-browser-use/memory/runbooks/<name>.py`.
3. Read the API sheet printed by `taskinit` (or run `obu api-sheet` once when
   resuming), then use `help(x)` for helpers whose exact
   signature, return shape, or ordering matters. The runtime docstring is the
   source of truth, not this file.

When an approach has failed twice, or the page is normal but data/events are
missing, enumerate all `techniques/*.md` under the memory root and read each
file's title, 关键词, and 触发信号 before another attempt.

**Done when:** the target's existing memory has been loaded and the helper
contracts needed for the next action are known.

### 3. Execute

Write every attempt as a real Python file in the task workspace and run it with:

```sh
obu exec ~/.open-browser-use/tasks/<domain>/<slug>-<unique>/02_fetch.py
```

The task workspace is the audit trail: failed attempts stay diffable and
tracebacks retain the file's own line numbers. `obu exec` runs the Python
subprocess with the task workspace (the script's parent directory) as its cwd;
the caller's cwd is unchanged. It supports normal Python, script-local imports
and `__file__`; pass script arguments with
`obu exec <file> -- <args...>`. Helpers are pre-imported and accept Python
arguments, so do not hand write JSON or shell serialization.

During browser work:

- List tabs before acting; use `user_tabs()` for existing user tabs and never
guess an id.
- Read the accessibility tree first; use `evaluate(js)` when the tree lacks
  the needed data.
- Act through `evaluate(js)` and verify the resulting state after every action.
- Wait for observable conditions with a bounded retry loop. Use a short settle
  delay only when no condition can be observed.
- For virtual-scrolled or incomplete DOM lists, prefer network capture with
  `wait_for_event` over scraping only rendered nodes.
- Use coordinates or raw CDP only when DOM/JS access is insufficient; inspect
  the relevant helper contract first.

OBU handles background-page readiness internally when attaching CDP, including
waking frozen pages and loading discarded pages without selecting the tab.
Do not add site-memory wake rituals, activate tabs, or create replacement tabs
just because a page is idle. A readiness failure is an OBU/browser diagnostic,
not a reason to replay the page action blindly.

Ownership persists across `obu exec` scripts within the current browser turn;
the local Python tab pointer resets, while the session remembers the active tab.
At each new turn, list tabs, explicitly `claim_tab()` the requested page, and
re-inspect its current state. Resume the same task workspace, not its old lock.
`user_tabs()` includes `ownerSessionId`: a different owner must release before
you claim. Never bypass a conflict by sharing its session ID or guessing that
an idle owner is dead.

**Done when:** the requested result is verified, or a bounded failure and its
observable cause have been recorded in the task workspace.

### 4. Finish: persist, verify, clean up

Before writing the final response, complete this sequence exactly once:

1. **Checkpoint.** If the task had a failed attempt, surprising page/API
   structure, hard-won fix, transferable technique, or verified repeatable
   flow, load [references/memory.md](references/memory.md) and persist it.
   A prose explanation of difficulties is not persistence.
2. **Choose the artifact.** Write one concise site fact per row with
   `memory_put(...)`; create/update a cross-site
   `~/.open-browser-use/memory/techniques/<name>.md`; or save a verified,
   repeatable script as
   `~/.open-browser-use/memory/runbooks/<name>.py`. If no expensive knowledge
   exists, explicitly decide that nothing should be written.
3. **Verify persistence.** Re-read every row/file written with native file
   tools. `memory_put()` only appends to `site-memory.lines`; it does not create
   technique or runbook files. A checkpoint is complete only when every chosen
   artifact exists and is readable, or the no-artifact decision is explicit.
4. **Organize and release browser state.** While still owning the pages, call
   `mark_deliverable(id)` only for pages that are themselves user-facing results,
   not original source pages merely read for a chat answer. Use `close_tab(id)`
   for your unneeded temporary pages. Do not close the user's original pages unless
   requested. Keep unfinished pages simply by not closing them. Finally call
   `release_tabs()` with no arguments to release all current ownership, even
   when waiting for login, approval or another turn. Check `failed` and
   `remaining`; cleanup is complete only when both are empty. Report partial
   failures and explicitly retry failed releases if appropriate. After successful
   release, do not use browser helpers again this turn; file work is allowed.

Use `kind="fail"` for a trap, `kind="surprise"` for unexpected structure, and
`kind="tip"` sparingly for a hard-won positive pattern. `fail` and `surprise`
require an actionable `unlock`. Never persist credentials, cookies, passwords,
private links, or routine success. Write a map of reusable site structure, not
a task diary.

**Done when:** persistence has been verified or explicitly ruled out, and
all current tab ownership has been released successfully (or a cleanup failure
has been explicitly reported).

### 5. Report

Only after the finish sequence, report the verified result, relevant
limitations, and unresolved failures. Claim that experience was saved only when
the corresponding row/file was re-read successfully.

**Done when:** the response distinguishes verified results, limitations, and
persisted experience.

## Safety

- Use the user's real Chrome without inspecting cookies, passwords, or session
  stores.
- Preserve the user's focus and window layout. Do not bring tabs forward, move
  or resize windows, or claim existing user tabs unless the user requested that
  page.
- Ask before uploads, clipboard access, form submissions, purchases, messages,
  deletions, or other externally visible changes.
- Stop at login walls and ask; signed-in SSO may be used as-is.
- Ask before installing extensions or launching browsers.

## References

- [references/tabs.md](references/tabs.md): tab lifecycle, claiming,
  deliverable results, file chooser, downloads, and clipboard details.
- [references/memory.md](references/memory.md): memory paths, artifact formats,
  checkpoint decision tree, deduplication, and staleness rules.
- [references/installation.md](references/installation.md): installation and
  verification.
- [references/troubleshooting.md](references/troubleshooting.md): connection
  failures and stale sockets.
