# Tabs: Ownership, Results, Files, Downloads, Clipboard

## Three independent concepts

- Task workspace: scripts, progress, browser/profile and stable session identity.
  It survives release; continue its scripts instead of creating another task.
- Tab ownership: explicit per-tab control held during browser work. Chrome tab
  groups and isolated windows are presentation, never ownership boundaries.
- Chrome page: can remain open after release, whether finished or unfinished.

## Claim and inspect

List `user_tabs()` first. It lists real tabs, including isolated-window pages,
with `ownerSessionId` (null when unowned). Match the requested URL/title; never
guess ids. `claim_tab(id)` acquires control and sets the current tab. Same-owner
claims are idempotent; different-owner claims fail with owner information.
Do not adopt another task's session ID to bypass the conflict. Ask that task to
release, or report unresolved ownership. There is no automatic stale-lock
recovery, lease, or force-claim API.

Background readiness belongs to OBU: on CDP attachment it loads a discarded
page in the background and wakes a resident frozen page without activating or
reloading it. A readiness timeout stops the caller before its requested CDP
action. The ownership stays tracked; check the release report rather than
opening replacement tabs or assuming a socket timeout means nothing happened.
CDP transport mode defaults to `OBU_CDP_MODE=direct` (direct CDP via browser root
WebSocket; unavailable Direct CDP is an explicit error). Use `OBU_CDP_MODE=auto`
to opt into automatic fallback to extension-mediated CDP, or
`OBU_CDP_MODE=extension` to force the extension route.

At a new turn, claim again and inspect: the user or another task may have
changed the page. Ownership spans multiple `obu exec` scripts in one browser
turn, not the lifetime of a Python process and not the whole unfinished task.

## Independent actions

- `release_tabs()` releases all tabs currently owned by this session.
  `release_tabs([id, ...])` releases only those ids; `release_tabs([])` does
  nothing. It detaches the debugger and clears control tracking, but does not
  close/move/ungroup pages or delete the task workspace.
  Return fields: `released`, `alreadyReleased`, `failed`, `remaining`.
  An already-unowned page is success; another owner's page is a failure.
  A deleted page's stale tracking is cleared. Partial failures are explicit:
  check `failed`, and for release-all also require `remaining` to be empty.
- `close_tab(id)` closes an owned page and clears its tracking. Claim first;
  unowned/other-owned pages cannot be closed through this helper. Only close
  agent temporary pages unless the user requested closing their original page.
- `mark_deliverable(id)` organizes an owned result page into the shared
  `✅ Open Browser Use` group. From an isolated task window it moves the result
  to an existing user window if available, without focusing it. Repeated marks
  converge on the same results group. Ownership is unchanged: release afterwards.
  Do not mark a source page merely because it was read. For a research answer
  delivered in chat, releasing the original source page is normally sufficient.

A completed task:

```python
mark_deliverable(result_tab)
close_tab(temporary_tab)
report = release_tabs()
assert not report["failed"] and not report["remaining"], report
```

Waiting for user input: leave the page open and call `release_tabs()`.
No separate task-pause status is needed.

Reload the updated extension before using the helpers. An old extension may
report an unsupported method; stop and update it rather than guessing a fallback.

## Files and clipboard

Uploads: start `wait_file_chooser`/`wait-file-chooser`, trigger the chooser,
then `set-file-chooser-files --file-chooser-id <id> --file /absolute/path`.
Waiters and chooser references are invalidated when ownership is released.

Downloads: observe via SDK notifications or `waitForDownload`/`downloadPath`.
Clipboard helpers operate through an owned page. Obtain the appropriate user
authorization for uploads, clipboard operations and other external effects.
