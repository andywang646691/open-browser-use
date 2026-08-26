---
name: open-browser-use
description: "Always use open-browser-use(OBU) for any web interaction: automation, scraping, testing, or site/app work."
---

# Open Browser Use

Thin CLI over the Chrome DevTools Protocol: knowing CDP means knowing this
tool — `cdp("Domain.method", **params)`, `evaluate(js)`, and a few tab
helpers on the user's real Chrome profile.

## Setup (one-time)

1. `obu ping`; if setup is missing, read [references/installation.md](references/installation.md).
2. `obu profiles --connected`: more than one target → ask the user which to
   use and list the names; never silently pick the active window. Chosen
   target not connected → ask the user to open it, retry; don't fall back.

## Start a task
State lives in env vars — set once, forget (flags still override):

```sh
export OBU_SESSION_ID="obu-<task-slug>"
export OBU_BROWSER="<browser>"   # after the user chose one
export OBU_PROFILE="<profile>"   # after the user chose one
# optional: opt out of the default isolated background window
# ("user" = open tabs in the user's focused window like before)
export OBU_WINDOW_MODE="user"
```
Don't switch browser/profile/session_id mid-task.

## Browser work
Work is done as real-Python heredoc scripts; helpers are pre-imported as plain
Python functions, so the whole language works (if/for, comprehensions,
f-strings, `len(...)`, slices). Values pass positionally or as keyword args —
never hand-written serialization.

```sh
obu <<'PY'
# your python here
PY
```

### Learn the API first — always
Don't write code from memory or from this file: the runtime API is the source
of truth. On the first script of a task — and any time you're unsure — discover
the surface from the helpers' own docstrings before writing real code:

```py
_help()      # list every available helper
help(name)   # read one helper's signature, params, return shape, gotchas
```

Every helper documents its contract in its docstring, so `help()` gives you the
exact, current API at runtime — including any helper you haven't seen before.
When a later call looks different than you expected, re-`help()` it instead of
guessing. This replaces the need for inline examples.

### How to use it
- **Process model**: each `obu <<'PY'` run is a fresh process. The current-tab
  pointer does not persist between runs — only tab membership persists via
  `OBU_SESSION_ID`. So reclaim or open a tab at the top of *every* script
  before any browser call.
- **Tabs**: never guess tab ids — list them first, then act on one. Session
  tabs open in a dedicated background window by default: unfocused,
  non-minimized, normal-sized (fills the screen), so the whole run never disturbs the user
  (`OBU_WINDOW_MODE="user"` opts back into the shared-window flow). Don't
  claim the user's existing tabs unless they explicitly asked for that page —
  `user_tabs()` already excludes OBU's own window so session tabs can't be
  mistaken for user tabs.
- **Read**: use the accessibility tree first; fall back to `evaluate(js)` only
  when the tree lacks the element.
- **Act**: via `evaluate(js)` — click, focus, set values — and always verify the
  result after acting.
- **Wait on conditions, not fixed sleeps**: when the state you need is
  observable, poll for it (loop with a bounded retry budget until it holds)
  instead of a blind `time.sleep`. Use a short settle delay only where no
  condition can be observed.
- **Observe CDP events for data the DOM won't show**: `wait_for_event(method,
  predicate=...)` blocks until a CDP event arrives (network responses, JS
  console errors, page events) and returns its params. Enable the domain
  first, then trigger the action, then wait — e.g. capture a timeline
  response instead of scraping a virtual-scrolled DOM:
  ```py
  cdp("Network.enable")
  evaluate("location.reload()")
  ev = wait_for_event("Network.responseReceived",
                      predicate=lambda e: "HomeTimeline" in e["params"]["response"]["url"])
  body = cdp("Network.getResponseBody", requestId=ev["requestId"])["body"]
  ```
  `help(wait_for_event)` for the event shape and timeout diagnostics.
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
  `runbooks/<name>.py` (verified code + header, rerun `obu < file`).
- Details, formats, decision tree, examples: [references/memory.md](references/memory.md).

## Hard rules
- The user's real Chrome: never inspect cookies, passwords, session stores.
- Ask before uploads, clipboard, form submits, purchases, sending, deleting, or other externally visible changes.
- Login walls: stop and ask; signed-in SSO may be used as-is.
- Ask before installing extensions or launching browsers.
- Never guess tab ids — list first, then act.

## References
Read when stuck, not up front:
- [references/tabs.md](references/tabs.md): tab lifecycle, claiming, deliverable/handoff, file choosers, downloads, clipboard.
- [references/installation.md](references/installation.md): install and verify.
- [references/troubleshooting.md](references/troubleshooting.md): connection failures, stale sockets.
