# Tabs: Lifecycle, Claiming, Files, Downloads, Clipboard

Read this when tab reuse, finalization, file upload, download, or clipboard
behavior matters. The main skill covers the default flow; this is the fine
print.

## Session Tabs

- Session tabs are the tabs Open Browser Use created or claimed for the
  current agent workflow. They live in a Chrome tab group named after the
  task (`<short task> - OBU`, fallback `Task - OBU`); set it with
  `name_session(name)` when the task has a real label.
- Use one unique `OBU_SESSION_ID` per agent task or conversation. Do not
  share the fallback `obu-cli` session across unrelated tasks — it is a
  manual convenience and can reuse stale task groups.

## Reuse Before Opening

1. List all user tabs with `user_tabs()` first.
2. If a tab's URL/title/group clearly matches the current task — including a
   tab in the shared `✅ Open Browser Use` deliverable group, an earlier
   handoff group, or a local dev server page the user has open
   (`localhost`, `127.0.0.1`, `*.local`, `*.test`) — `claim_tab(id)` it and
   continue from there instead of opening a duplicate.
3. Only open a new tab when no suitable tab exists. Match dev servers on
   host, port, and path or title; ask the user when ambiguous.
4. Do not claim unrelated deliverable tabs just because they sit in the
   deliverable group. A deliverable tab can be claimed back into a new
   session, worked on, and finalized as `deliverable` again when it stays
   the user-facing result — repeated work on the same page converges to one
   live tab.

## Finalization

- `finalize_tabs()` runs exactly once, as the last browser action of the
  turn; no browser tools afterwards. Default `keep` is empty: task tabs
  close.
- Keep a tab only when the user needs that live page after the turn:
  - `"status": "deliverable"` — the tab itself is the user-facing output or
    requested open page (created/edited document, dashboard, checkout/cart,
    submitted form result, a page the user asked to inspect directly).
    Deliverable tabs move to the shared `✅ Open Browser Use` tab group.
  - `"status": "handoff"` — the task is still in progress and a later turn
    continues from this page (waiting for user input, login, approval,
    payment, CAPTCHA, unfinished workflow). Handoff tabs stay in the task
    session group.
- Omit research, source, search, intermediate, duplicate, blank, error, and
  login/navigation tabs after extracting what you need.

## File Choosers

Uploads use the intercepted file chooser flow: start waiting, trigger the
chooser in the page, then set absolute local paths.

1. `wait_file_chooser(tab_id=...)` (or `wait-file-chooser --tab-id`) starts
   waiting.
2. Trigger the file picker in the page, usually with a click driven by CDP.
3. `set-file-chooser-files --file-chooser-id <id> --file /absolute/path` —
   repeat `--file` (or comma-separate) for multiple files.

## Downloads

Observe downloads with SDK notification handlers (`onDownloadChange`) or the
`waitForDownload` / `downloadPath` methods. Downloads only fire notifications
while a browser session is active.

## Clipboard

Clipboard helpers (`readClipboard`, `writeClipboard`, and text variants)
operate through the current controlled tab and are sensitive user actions —
ask before reading or writing clipboard data.