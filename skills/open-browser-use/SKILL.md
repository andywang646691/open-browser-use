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
export OBU_SESSION_ID="obu-<task-slug>-<timestamp>"
export OBU_BROWSER="<browser>"   # after the user chose one
export OBU_PROFILE="<profile>"   # after the user chose one
```
Don't switch browser/profile mid-task.

## Browser work
Real-Python heredoc scripts: helpers are pre-imported as plain Python
functions, and the whole language works (if/for, comprehensions, f-strings).
Values pass positionally or as keyword args — never hand-written
serialization:

```sh
obu <<'PY'
tabs = user_tabs()
claim_tab(tabs[0]["id"])          # reuse a matching open tab, don't duplicate
open_tab("https://example.com")   # sets the current tab
tree = cdp("Accessibility.getFullAXTree")["nodes"]  # role / name / backendDOMNodeId
print(evaluate("document.querySelector('h1').textContent"))
evaluate("document.querySelector('a').click()")     # act via JS
headings = [n for n in tree if n["role"]["value"] == "heading"]  # real Python
finalize_tabs()                   # keep empty by default
PY
```
- Each heredoc run is a fresh process: the current-tab pointer does not carry
  over between runs — only tab membership persists via `OBU_SESSION_ID`.
  Start every script with `claim_tab(id)` or `open_tab(url)` before any
  `cdp()`/`evaluate()`.
- Every helper documents its signature and return shape in its docstring:
  unsure, call `help(name)` or `_help()` inside the script — the contract is
  always available at runtime.
- Find elements via the AX tree first; fall back to `evaluate(js)` when the
  tree lacks the element. Never guess tab ids.
- Act via `evaluate(js)` — `.click()`, focus, set values — and verify after
  acting. For coordinates run `cdp("DOM.getDocument")` once, then
  `cdp("DOM.getBoxModel", backendNodeId=n)["model"]["content"]` before
  scrolling or `Input.dispatchMouseEvent` (raw CDP, can be dropped on
  background tabs).
- Other helpers: `navigate(tab_id, url)`, `history(query)`, `tabs()`,
  `name_session(name)` — Python builtins like `len(...)` and slices
  (`tree[0:5]`) work natively.

## End a task
`finalize_tabs()` exactly once, last browser action; keep empty by default.
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
