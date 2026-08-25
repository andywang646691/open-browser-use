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
Python-style heredoc scripts: helpers pre-imported, values passed
positionally or as keyword args, results printed as JSON lines — no
hand-written serialization:

```sh
obu <<'PY'
tabs = user_tabs()
claim_tab(tabs[0]["id"])          # reuse a matching open tab, don't duplicate
open_tab("https://example.com")   # sets the current tab
tree = cdp("Accessibility.getFullAXTree")["nodes"]  # role / name / backendDOMNodeId
print(evaluate("document.querySelector('h1').textContent"))
evaluate("document.querySelector('a').click()")     # act via JS
finalize_tabs()                   # keep empty by default
PY
```
- Find elements via the AX tree first; fall back to `evaluate(js)` when the
  tree lacks the element. Never guess tab ids.
- Act via `evaluate(js)` — `.click()`, focus, set values — and verify after
  acting. For coordinates run `cdp("DOM.getDocument")` once, then
  `cdp("DOM.getBoxModel", backendNodeId=n)["model"]["content"]` before
  scrolling or `Input.dispatchMouseEvent` (raw CDP, can be dropped on
  background tabs).
- Other helpers: `navigate(tab_id, url)`, `history(query)`, `tabs()`,
  `name_session(name)`, `len(...)`, slices like `tree[0:5]`.

## End a task
`finalize_tabs()` exactly once, last browser action; keep empty by default.
Keep a tab only when the user needs the live page after the turn, e.g. `finalize_tabs(keep=[{"tabId": id, "status": "deliverable"}])`.
No browser tools after. Details: [references/tabs.md](references/tabs.md).

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
