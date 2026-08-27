---
name: open-browser-use
description: "Always use open-browser-use(OBU) for any web interaction: automation, scraping, testing, or site/app work."
---

# Open Browser Use

Thin CLI over the Chrome DevTools Protocol: knowing CDP means knowing this
tool — `cdp("Domain.method", **params)`, `evaluate(js)`, and a few tab
helpers on the user's real Chrome profile.

## Setup (per task — one command)

```sh
obu taskinit <domain>/<slug> [--browser X --profile Y]
```

One command for the whole startup ritual: creates the task workspace, sets
session/browser/profile for the task, and prints the api-sheet.
`obu exec <file>` then uses that setup automatically. Many connected
targets → it stops and lists them: ask the user which to use, never silently
pick, then rerun with `--browser/--profile`.

Each run creates a fresh time-stamped instance — the printed path is the one
to work in. To continue an old task, `obu exec` its scripts directly (its
setup loads automatically); only start a new instance with taskinit.

## Start a task
Prefer `obu taskinit` (Setup above) — it sets these for you, and
`obu exec` uses them automatically. Manual state also works (flags still
override):

```sh
export OBU_SESSION_ID="obu-<task-slug>"
export OBU_BROWSER="<browser>"   # after the user chose one
export OBU_PROFILE="<profile>"   # after the user chose one
```
Don't switch browser/profile/session_id mid-task.

## Task workspace
Every script for this task lives under
`~/.open-browser-use/tasks/<domain>/<slug>/` (slug = OBU_SESSION_ID suffix;
`$OBU_TASK_DIR` overrides the root). Run it with `obu exec path.py` so
failed attempts stay on disk, tracebacks carry the file's own line numbers,
and the flow becomes an audit trail; a stable flow at wrap-up can be promoted
to a runbook (see references/memory.md).

## Browser work
Task scripts are real-Python files; helpers are pre-imported as plain Python
functions, so the whole language works (if/for, comprehensions, f-strings,
`len(...)`, slices). Values pass positionally or as keyword args — never
hand-written serialization. Write each script in the task workspace above,
then run it as a file:

```sh
obu exec ~/.open-browser-use/tasks/<domain>/<slug>/02_fetch.py
```

Tracebacks report the file's own line numbers — the file is the source of
truth, so failed attempts stay diffable on disk.

### Learn the API first — always
Don't write code from memory or from this file: the runtime API is the source
of truth. Run `obu api-sheet` once (one line per helper: signature + doc
line), then `help(x)` only for the helper you're actually about to use —
signature, params, return shape, gotchas. Both `help(finalize_tabs)` and
`help("finalize_tabs")` work.

Don't spend a script run just to learn the API: fold a `print(help(x))` into
the script you're writing. Every helper carries its contract in its
docstring, so docstrings are always current — when a call looks different
than expected, re-`help()` it instead of guessing.

### How to use it
- **Process model**: each `obu exec <file>` run is a fresh process and the
  local tab pointer resets, but the session's *last active tab* persists —
  `evaluate()`/`cdp()` resume it automatically, so you usually don't need to
  reclaim a tab. Explicit `open_tab()`/`claim_tab()` is for picking a
  *different* tab: target one URL among several session tabs, take over a
  user tab (`user_tabs()`), or work in a session with no tabs yet. If the
  session has no tabs at all (e.g. `finalize_tabs()` already closed them),
  browser calls error with "no current tab" — that means there is genuinely
  nothing to work on.
- **Tabs**: never guess tab ids — list them first, then act on one. Session
  tabs open in the user's Chrome window by default — don't claim the user's
  existing tabs unless they explicitly asked for that page — `user_tabs()`
  lists them; session tabs are your own.
- For full data of virtual-scrolled lists, the network-capture pattern
  (`wait_for_event`) remains the robust data-plane choice: it returns every
  response, not just what the DOM renders.
- **Read**: use the accessibility tree first; fall back to `evaluate(js)` only
  when the tree lacks the element.
- **Act**: via `evaluate(js)` — click, focus, set values — and always verify the
  result after acting.
- **Wait on conditions, not fixed sleeps**: when the state you need is
  observable, poll for it (loop with a bounded retry budget until it holds)
  instead of a blind `time.sleep`. Use a short settle delay only where no
  condition can be observed.
- **Coordinates / raw CDP**: only when DOM/JS access isn't enough; `help()` on
  the relevant helpers for the exact steps and their order.

## End a task
`finalize_tabs()` exactly once, last browser action; keep empty by default.
It returns what it closed (`closed`/`released`/`kept`) — a `warning` means
nothing was closed: re-verify with `user_tabs()` and clean leftovers via
`close_tab(id)`.
Keep a tab only when the user needs the live page after the turn, e.g. `finalize_tabs(keep=[{"tabId": id, "status": "deliverable"}])`.
No browser tools after. Details: [references/tabs.md](references/tabs.md).

## Experience memory (site memory)
Deposit each visit's hard-won knowledge so the next agent reuses it; the
skill surface stays 1 and only data grows (never per-site skills). Store:
`~/.open-browser-use/memory/` (override: `$OBU_MEMORY_DIR`). Plain files —
no memory commands; read with your own tools, write via one helper.

- Before a domain new to this session, pre-fetch: `grep "domain=<domain>|"
  site-memory.lines` (+ playbook, runbooks, `tech=` links). Write a line at
  wrap-up with `memory_put(...)`; introspect `help(memory_put)` for kinds.
- On repeated failure (≥2 tries, or page normal but data/events silently
  missing), unconditionally read the whole techniques/ index first — don't
  guess keywords, don't grind more rounds.
- Record only "expensive" knowledge: traps, surprises, unlocks, transferable
  fixes, working code. Routine success isn't worth a row; no credentials.
- Transferable fix → `techniques/<name>.md`; stable repeat flow →
  `runbooks/<name>.py` (verified code + header, rerun `obu exec <file>`).
- Details, formats, decision tree, examples: [references/memory.md](references/memory.md).

## Hard rules
- The user's real Chrome: never inspect cookies, passwords, session stores.
- Never steal focus or visibly rearrange the user's windows/tabs
  (bringToFront, moves, resizes, tabs in their focused window). If a task
  seems to require it, try a non-disruptive alternative first;
  if none exists, ask the user before doing it.
- Ask before uploads, clipboard, form submits, purchases, sending, deleting, or other externally visible changes.
- Login walls: stop and ask; signed-in SSO may be used as-is.
- Ask before installing extensions or launching browsers.
- Never guess tab ids — list first, then act.

## References
Read when stuck, not up front:
- [references/tabs.md](references/tabs.md): tab lifecycle, claiming, deliverable/handoff, file choosers, downloads, clipboard.
- [references/installation.md](references/installation.md): install and verify.
- [references/troubleshooting.md](references/troubleshooting.md): connection failures, stale sockets.
