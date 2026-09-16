---
name: open-browser-use
description: "Use Open Browser Use (OBU) for browser automation, web scraping, site testing, or any task that reads or changes a web page in the user's Chrome."
---

# Open Browser Use

Use OBU's real Chrome session through CDP: `cdp("Domain.method", **params)`,
`evaluate(js)`, and the tab helpers below. Extension lifecycle helpers
(`list_extensions`, `install_extension`, `reload_extension`,
`trigger_extension_action`, `uninstall_extension`) mirror the Chrome DevTools
MCP Extension category and require Direct CDP support; they manage unpacked
extensions only.

## Process

### 1. Bootstrap

For a new task, run one setup command:

```sh
obu taskinit <domain>/<slug> [--browser X --profile Y]
```

This creates the task workspace, selects the browser/profile, and hydrates the
execution context in the same output. The bootstrap includes:

- only user tabs whose normalized URL host exactly matches the task domain
  (`www.` is normalized away), including each tab's `ownerSessionId`;
- matching `site-memory.lines` rows plus explicitly linked technique/runbook
  content, bounded and marked when truncated or unavailable; and
- the runtime API sheet.

`taskinit` does not claim tabs and never executes linked runbooks. Keep both
identities it prints: `taskRef=<domain>/<instance>` is the CLI handle, while
`dir=<absolute task workspace>` is where task files must be written. Never
construct either instance suffix yourself. If multiple targets are connected,
stop and ask the user which target to use; never silently choose one. To
continue an existing task, reuse its taskRef/workspace instead of starting a
new `taskinit` instance.

Do not repeat the old startup ceremony when bootstrap data is sufficient: do
not immediately call `user_tabs()`, grep `site-memory.lines`, or reread linked
artifacts that `taskinit` already printed. Call `user_tabs()` only as a fallback
when no bootstrap tab matches, the task intentionally crosses domains, or a
fresh full tab view is required. If a linked artifact was truncated and its
remaining content matters, read that file directly. When an approach has
failed twice, or the page is normal but data/events are missing, enumerate all
`techniques/*.md` under the memory root and read each file's title, 关键词, and
触发信号 before another attempt.

When resuming an existing task, run `obu api-sheet` once if the runtime API is
not already known, and use `help(x)` when an exact helper signature, return
shape, or ordering matters. Runtime docstrings are the source of truth.

**Done when:** the taskRef, absolute task directory, selected target, relevant
tab ownership, existing domain memory, and helper contracts needed for the next
action are known.

### 2. Execute

Write every attempt as a real Python file in the task workspace and run it with:

```sh
obu exec --task <taskRef> 02_fetch.py
```

Write files using the absolute `dir` printed by `taskinit`, for example
`<dir>/02_fetch.py`. Execute them by taskRef; do not rely on shell cwd and do
not repeat the workspace path in every exec call. `obu exec --task` resolves
the workspace, loads that task's `.obu.env`, and runs the script with the task
workspace as Python cwd. With a custom task root, pass the printed root as
`--task-root <root>` unless the same `$OBU_TASK_DIR` is already inherited.
The legacy `obu exec <path>` form remains available for compatibility. Script
arguments follow `--`, e.g. `obu exec --task <taskRef> 02_fetch.py -- <args...>`.
Helpers are pre-imported and accept Python arguments, so do not hand write JSON
or shell serialization.

During browser work:

- On a new task, choose from the domain-matched tabs already printed by
  `taskinit`; do not relist all user tabs first. Use `user_tabs()` only when the
  bootstrap set is insufficient or when a later turn needs a fresh ownership
  view. Never guess an id.
- Read the accessibility tree first; use `evaluate(js)` when the tree lacks
  the needed data.
- Act through `evaluate(js)` and verify the resulting state after every action.
- Wait for observable conditions with a bounded retry loop. Use a short settle
  delay only when no condition can be observed.
- For virtual-scrolled or incomplete DOM lists, prefer network capture with
  `wait_for_event` over scraping only rendered nodes.
- Use coordinates or raw CDP only when DOM/JS access is insufficient; inspect
  the relevant helper contract first.
- Before installing or uninstalling an extension, confirm the user requested
  that browser state change. Use an absolute unpacked extension directory;
  after installation, use the returned id and an owned tab for
  `trigger_extension_action`.

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

### 3. Finish: persist, verify, clean up

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
4. **Organize and release browser state.** Once the requested result is verified,
   always perform a deliverable pass before cleanup: while still owning the pages,
   identify every tab that is itself a user-facing result and call
   `mark_deliverable(id)` for each one. Do not finish a completed browser task by
   calling only `release_tabs()`. Source-only pages that were merely read for a
   chat answer are not deliverables and must not be marked. If no owned page is
   itself a user-facing result, explicitly note `no deliverable tab` and continue.
   Use `close_tab(id)` for your unneeded temporary pages. Do not close the user's
   original pages unless requested. Keep unfinished pages simply by not closing
   them. Finally call `release_tabs()` with no arguments to release all current
   ownership, even when waiting for login, approval or another turn. Check
   `failed` and `remaining`; cleanup is complete only when both are empty. Report
   partial failures and explicitly retry failed releases if appropriate. After
   successful release, do not use browser helpers again this turn; file work is
   allowed.

Use `kind="fail"` for a trap, `kind="surprise"` for unexpected structure, and
`kind="tip"` sparingly for a hard-won positive pattern. `fail` and `surprise`
require an actionable `unlock`. Never persist credentials, cookies, passwords,
private links, or routine success. Write a map of reusable site structure, not
a task diary.

**Done when:** persistence has been verified or explicitly ruled out, every
user-facing result tab has been marked with `mark_deliverable` (or `no
deliverable tab` was recorded), and all current tab ownership has been released
successfully (or a cleanup failure has been explicitly reported).

### 4. Report

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
- Ask before installing extensions or launching browsers. Extension lifecycle
  helpers may require `OBU_CDP_MODE=direct` or `auto` and a Chrome launch with
  unsafe extension debugging enabled; unsupported Chrome configurations should
  fail explicitly rather than silently falling back.

## References

- [references/tabs.md](references/tabs.md): tab lifecycle, claiming,
  deliverable results, file chooser, downloads, and clipboard details.
- [references/memory.md](references/memory.md): memory paths, artifact formats,
  checkpoint decision tree, deduplication, and staleness rules.
- [references/installation.md](references/installation.md): installation and
  verification.
- [references/troubleshooting.md](references/troubleshooting.md): connection
  failures and stale sockets.
