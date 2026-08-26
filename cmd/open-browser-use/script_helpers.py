"""obu heredoc prelude — pre-imported helpers for `obu <<'PY' ... PY`.

This module is embedded in the obu binary (go:embed) and executed together
with the user's script inside a real Python interpreter, so the full Python
language is available: if/for, comprehensions, f-strings, imports, etc.

Every helper talks to the obu native host over the Browser Use JSON-RPC
Unix socket (4-byte native-endian frame length + JSON payload). The socket
path is resolved by the Go CLI and passed via OBU_SCRIPT_SOCKET.

API contract lives in these docstrings: when unsure about a signature or a
return shape, call help(name) or name.__doc__ inside your script.
"""

import json
import os
import socket
import struct
import sys
import time
from collections import deque

_SESSION_ID = os.environ.get("OBU_SESSION_ID", "obu-cli")
_TURN_ID = "{}-{}".format(_SESSION_ID, time.time_ns())
# Default window placement for session tabs: "user" opts out into the legacy
# shared-window behavior; anything else (or unset) means the dedicated
# isolated window. Injected when the caller did not pass window_mode itself.
_WINDOW_MODE = os.environ.get("OBU_WINDOW_MODE", "")
_SOCKET_PATH = os.environ.get("OBU_SCRIPT_SOCKET", "")
_SOCKET_ERROR = os.environ.get("OBU_SCRIPT_SOCKET_ERROR", "")
_CONN = None
_CURRENT_TAB = None
_NEXT_REQ_ID = 0
_TIMEOUT = float(os.environ.get("OBU_SCRIPT_TIMEOUT_SECONDS", "10"))
# Bounded tail of id-less notification frames (onCDPEvent / heartbeat) that
# arrived while a request was being answered. wait_for_event() drains it first,
# so events that landed during other helper calls are not lost.
_EVENT_QUEUE = deque(maxlen=500)


def _read_exact(conn, count):
    chunks = []
    remaining = count
    while remaining > 0:
        chunk = conn.recv(remaining)
        if not chunk:
            raise RuntimeError("obu socket closed mid-frame")
        chunks.append(chunk)
        remaining -= len(chunk)
    return b"".join(chunks)


def _get_conn():
    """Lazily open the host socket and return it."""
    global _CONN
    if _SOCKET_ERROR:
        raise RuntimeError("obu: " + _SOCKET_ERROR)
    if not _SOCKET_PATH:
        raise RuntimeError(
            "no obu socket: env OBU_SCRIPT_SOCKET is empty (run through `obu`, not directly)"
        )
    if _CONN is None:
        _CONN = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        _CONN.settimeout(_TIMEOUT)
        try:
            _CONN.connect(_SOCKET_PATH)
        except OSError as exc:
            raise RuntimeError("cannot connect to obu socket {}: {}".format(_SOCKET_PATH, exc))
    return _CONN


def _request(method, params=None):
    """Send one JSON-RPC request and return the matching result payload.

    Frames are matched by request id: id-less notification frames (the relay's
    onCDPEvent/heartbeat broadcasts) are queued for wait_for_event() instead of
    being dropped; any other stray frames are skipped, never misread as this
    request's result.
    """
    global _NEXT_REQ_ID
    if _SOCKET_ERROR:
        raise RuntimeError("obu: " + _SOCKET_ERROR)
    conn = _get_conn()
    _NEXT_REQ_ID += 1
    request_id = "obu-script-{}".format(_NEXT_REQ_ID)
    request = {
        "jsonrpc": "2.0",
        "id": request_id,
        "method": method,
        "params": dict(params or {}),
    }
    request["params"].setdefault("session_id", _SESSION_ID)
    request["params"].setdefault("turn_id", _TURN_ID)
    if _WINDOW_MODE:
        request["params"].setdefault("window_mode", _WINDOW_MODE)
    payload = json.dumps(request).encode("utf-8")
    _CONN.sendall(struct.pack("@I", len(payload)) + payload)
    while True:
        header = _read_exact(_CONN, 4)
        length = struct.unpack("@I", header)[0]
        body = json.loads(_read_exact(_CONN, length).decode("utf-8"))
        if body.get("id") != request_id:
            if not body.get("id"):
                # Id-less notification (onCDPEvent/heartbeat broadcast): keep
                # it for wait_for_event() instead of dropping it.
                _EVENT_QUEUE.append(body)
            # Another client's response frame: skip and keep reading until
            # this request's response arrives.
            continue
        if "error" in body:
            message = body["error"]
            if isinstance(message, dict):
                message = message.get("message", str(message))
            raise RuntimeError("obu {}: {}".format(method, message))
        return body.get("result")


def _coerce_tab_id(tab_id, helper):
    """Normalize a tab id to a positive int, tolerating numeric strings.

    Chrome tab ids come back from user_tabs()/tabs() as ints, but scripts
    sometimes hold them as strings (JSON round-trips, model output). Accept
    both; reject anything else early with a message that points at
    user_tabs(), instead of a cryptic "requires an integer tabId" from the
    extension downstream.
    """
    if isinstance(tab_id, bool) or not isinstance(tab_id, (int, str)):
        raise RuntimeError(
            "obu {}: tab id must be an int (e.g. from user_tabs()), got {!r}".format(
                helper, tab_id
            )
        )
    if isinstance(tab_id, str):
        digits = tab_id.strip()
        if not digits.isdigit():
            raise RuntimeError(
                "obu {}: tab id must be an integer, got {!r}; list ids with user_tabs()".format(
                    helper, tab_id
                )
            )
        tab_id = int(digits)
    if tab_id <= 0:
        raise RuntimeError(
            "obu {}: tab id must be a positive integer, got {!r}".format(helper, tab_id)
        )
    return tab_id


def _attach(tab_id):
    _request("attach", {"tabId": tab_id})


def user_tabs():
    """List the user's real Chrome tabs.

    Returns a list of tab objects: [{"id": int, "url": str, "title": str, ...}].
    Never guess tab ids — list first, then claim.

    Example: tabs = user_tabs(); print(tabs[0]["id"])"""
    return _request("getUserTabs")


def tabs():
    """List the current obu session's tabs.

    Returns a list of session tab objects: [{"id": int, "url": str, "title": str}].
    Only tabs opened/claimed in this session appear here."""
    return _request("getTabs")


def claim_tab(tab_id):
    """Take over an existing Chrome tab for this session and make it current.

    tab_id: int (or numeric string) from user_tabs().
    Returns the claimed tab object. Afterwards cdp()/evaluate() act on it.

    Example: claim_tab(user_tabs()[0]["id"]) / claim_tab("279925170")"""
    global _CURRENT_TAB
    tab_id = _coerce_tab_id(tab_id, "claim_tab")
    result = _request("claimUserTab", {"tabId": tab_id})
    _CURRENT_TAB = tab_id
    return result


def open_tab(url="", window_mode=None):
    """Open a new session tab (optional url) and make it current.

    window_mode: "isolated" (default; dedicated background window that never
    steals focus) or "user" (legacy: open in the focused user window).
    Omit to inherit the OBU_WINDOW_MODE environment variable.

    Returns the new tab object {"id": int, "url": str, ...}; the id is the
    current tab for later cdp()/evaluate() calls.

    Example: tab = open_tab("https://example.com")
             tab = open_tab("https://example.com", window_mode="user")"""
    global _CURRENT_TAB
    params = {}
    if window_mode is not None:
        params["window_mode"] = window_mode
    tab = _request("createTab", params or None)
    tab_id = tab.get("id")
    if not isinstance(tab_id, int):
        raise RuntimeError("createTab returned no numeric id: {!r}".format(tab))
    _CURRENT_TAB = tab_id
    if url:
        _attach(tab_id)
        _request(
            "executeCdp",
            {
                "target": {"tabId": tab_id},
                "method": "Page.navigate",
                "commandParams": {"url": url},
            },
        )
    return tab


def navigate(tab_id, url):
    """Navigate an existing session tab to url (attaches it first).

    tab_id: int (or numeric string) of a session tab.
    Returns the Page.navigate result. Prefer open_tab(url) for new tabs.

    Example: navigate(tab_id, "https://example.com")"""
    tab_id = _coerce_tab_id(tab_id, "navigate")
    _attach(tab_id)
    return _request(
        "executeCdp",
        {
            "target": {"tabId": tab_id},
            "method": "Page.navigate",
            "commandParams": {"url": url},
        },
    )


def history(query, limit=100):
    """Search the user's Chrome history.

    query: str substring match; limit: int (default 100).
    Returns a list of history entries, newest first.

    Example: history("browser use", limit=20)"""
    return _request("getUserHistory", {"query": query, "limit": limit})


def name_session(name):
    """Name the current session's Chrome tab group (shown as the group title).

    Example: name_session("Docs scan - OBU")"""
    return _request("nameSession", {"name": name})


def cdp(method, **params):
    """Run a raw Chrome DevTools Protocol command on the current tab.

    method: CDP domain.method, e.g. "Accessibility.getFullAXTree",
    "DOM.getBoxModel", "Runtime.evaluate". params become the CDP command
    params directly (use backendNodeId=123 style kwargs, never JSON strings).

    Returns the CDP result object, e.g. cdp("Accessibility.getFullAXTree")
    -> {"nodes": [{"role": {"value": "heading"}, "name": {"value": "..."},
    "backendDOMNodeId": int, ...}]}.

    Requires a current tab: call open_tab()/claim_tab() first.

    Example: nodes = cdp("Accessibility.getFullAXTree")["nodes"]"""
    if _CURRENT_TAB is None:
        raise RuntimeError("no current tab: call open_tab(url) or claim_tab(id) first")
    _attach(_CURRENT_TAB)
    return _request(
        "executeCdp",
        {"target": {"tabId": _CURRENT_TAB}, "method": method, "commandParams": dict(params)},
    )


def evaluate(js):
    """Run a JavaScript expression in the current tab and return its value.

    js: str expression; returnByValue is set automatically. Use for DOM
    reads and actions (.click(), focus, set value) that coordinates cannot
    express. Raises on JS exceptions.

    Example: text = evaluate("document.querySelector('h1').textContent")"""
    if _CURRENT_TAB is None:
        raise RuntimeError("no current tab: call open_tab(url) or claim_tab(id) first")
    _attach(_CURRENT_TAB)
    response = _request(
        "executeCdp",
        {
            "target": {"tabId": _CURRENT_TAB},
            "method": "Runtime.evaluate",
            "commandParams": {"expression": js, "returnByValue": True},
        },
    )
    if response.get("exceptionDetails"):
        raise RuntimeError("evaluate threw: {!r}".format(response["exceptionDetails"]))
    remote = response.get("result") or {}
    return remote.get("value")


def wait_for_event(method, predicate=None, timeout=30.0):
    """Block until a CDP event with the given method arrives; return its params.

    Events are the id-less notification frames the relay broadcasts (e.g.
    onCDPEvent). Only events received while this script process is alive can be
    observed, so the typical shape is: enable the CDP domain you need, trigger
    the action (reload / click / navigate), then wait:

        cdp("Network.enable")
        evaluate("location.reload()")
        ev = wait_for_event("Network.responseReceived",
                            predicate=lambda e: "HomeTimeline" in e["params"]["response"]["url"])
        body = cdp("Network.getResponseBody", requestId=ev["requestId"])["body"]

    method: CDP event name, e.g. "Network.responseReceived",
    "Runtime.consoleAPICalled", "Page.loadEventFired".
    predicate: optional callable(event) -> bool, where event is
    {"source": {"tabId": int, ...}, "method": str, "params": dict}; omit to
    match any event of that method.

    Returns the inner params dict of the first matching event. Raises after
    `timeout` seconds with a diagnostic listing the events that were seen, so a
    missing-timeout (e.g. forgot to cdp("Network.enable")) is self-explanatory.

    While blocked, this helper reads the host socket itself: do not fire other
    helpers from another thread concurrently.
    """
    if not isinstance(method, str) or not method:
        raise ValueError("method is required, e.g. 'Network.responseReceived'")
    timeout = float(timeout)
    if timeout <= 0:
        raise ValueError("timeout must be > 0, got {!r}".format(timeout))

    conn = _get_conn()
    seen = {}

    def _incoming(frame):
        """Map one raw frame to a CDP event, or None if it is not a match."""
        if frame.get("method") != "onCDPEvent":
            return None
        params = frame.get("params") or {}
        if params.get("method") != method:
            return None
        event = {"source": params.get("source"), "method": method, "params": params.get("params")}
        if predicate is None:
            return event
        try:
            return event if predicate(event) else None
        except Exception as exc:
            raise RuntimeError("wait_for_event predicate raised: {!r}".format(exc))

    def _count(frame):
        params = frame.get("params") or {}
        name = (
            params.get("method")
            if frame.get("method") == "onCDPEvent"
            else (frame.get("method") or "frame")
        )
        seen[name] = seen.get(name, 0) + 1

    def _drain():
        """Consume queued frames (FIFO) until a match; count the rest."""
        while _EVENT_QUEUE:
            frame = _EVENT_QUEUE.popleft()
            match = _incoming(frame)
            if match is not None:
                return match
            _count(frame)
        return None

    match = _drain()
    deadline = time.monotonic() + timeout
    while match is None:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            break
        conn.settimeout(remaining)
        try:
            header = _read_exact(conn, 4)
            if not header:
                raise RuntimeError(
                    "obu socket closed while waiting for event {!r}".format(method)
                )
            length = struct.unpack("@I", header)[0]
            body = json.loads(_read_exact(conn, length).decode("utf-8"))
        except socket.timeout:
            break
        if body.get("id"):
            # Response to a request from another thread/session: not ours.
            continue
        match = _incoming(body)
        if match is None:
            _count(body)
    conn.settimeout(_TIMEOUT)
    if match is not None:
        return match["params"]
    top = ", ".join(
        "{} x{}".format(name, count)
        for name, count in sorted(seen.items(), key=lambda kv: -kv[1])[:5]
    ) or "(none)"
    hint = ""
    if method.startswith("Network.") and not any(k.startswith("Network.") for k in seen):
        hint = ' ; hint: call cdp(\"Network.enable\") before the action that fires the event'
    raise RuntimeError(
        "wait_for_event({!r}) timed out after {:.1f}s: received {} events, none matched "
        "(methods seen: {}){}".format(method, timeout, sum(seen.values()), top, hint)
    )


def finalize_tabs(keep=None):
    """Close the session's non-kept tabs. Call exactly once as the last step.

    keep: list of {"tabId": int, "status": "deliverable"|"handoff"} tabs to
    keep open; defaults to [] (close everything). Keep a tab only when the
    user needs the live page after this turn.

    Returns a report so you can verify instead of trusting silence:
      {"closed": [tabId...], "released": [tabId...],
       "kept": [{"tabId": id, "status": ...}]}
    A "warning" key means nothing was closed (session state lost / tab group
    dissolved): verify with user_tabs() and clean leftovers via close_tab().

    Example: finalize_tabs()  # close all
    Example: finalize_tabs(keep=[{"tabId": 123, "status": "deliverable"}])"""
    return _request("finalizeTabs", {"keep": list(keep or [])})


def close_tab(tab_id):
    """Close any real Chrome tab by id — cleanup fallback when finalize_tabs()
    reports a warning or a stray tab survives.

    tab_id: int (or numeric string) from user_tabs(). Never guess ids.
    Returns {"closed": tab_id}. chrome:// pages are refused.

    Example: close_tab(user_tabs()[0]["id"])"""
    tab_id = _coerce_tab_id(tab_id, "close_tab")
    return _request("closeUserTab", {"tabId": tab_id})


def memory_put(domain, kind, note, unlock=None, selector=None, tech=None, runbook=None):
    """Record one reusable fact about a site to the site memory store.

    Appends one labeled line to ~/.open-browser-use/memory/site-memory.lines
    (override the root with OBU_MEMORY_DIR). Read it back later by grepping
    'domain=<domain>|' in that file. Append-only: never overwrites, and
    concurrent puts never clobber each other.

    kind: 'fail' (a trap you hit), 'surprise' (unexpected structure), or
          'tip' (a hard-won positive pattern; use sparingly).
    note: the actionable fact, one sentence. unlock: how you got past it
          (required for kind fail/surprise).
    selector / tech / runbook: optional links — a verified selector, the
    techniques/<name>.md pattern this lines up with, or a runbooks/<name>.py
    script. See references/memory.md for the full convention.
    Field values must not contain '|'; domains are normalized (www. dropped,
    lowercased). Raises ValueError on invalid input.

    Example:
        memory_put("x.com", kind="surprise",
                   note="timeline is virtual-scroll, DOM renders only visible items",
                   unlock="scrape network requests instead of scrolling",
                   tech="virtual-scroll-throttle")
    """
    if kind not in ("fail", "surprise", "tip"):
        raise ValueError("kind must be 'fail', 'surprise' or 'tip', got {!r}".format(kind))
    note = str(note or "").strip()
    if not note:
        raise ValueError("note is required")
    if kind in ("fail", "surprise") and not unlock:
        raise ValueError("unlock is required for kind={!r}".format(kind))
    domain = str(domain or "").strip().lower()
    if not domain:
        raise ValueError("domain is required")
    if domain.startswith("www."):
        domain = domain[len("www."):]
    fields = [
        ("domain", domain),
        ("ts", time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())),
        ("kind", kind),
        ("note", note),
    ]
    for label, value in (("unlock", unlock), ("selector", selector),
                         ("tech", tech), ("runbook", runbook)):
        if value is not None:
            fields.append((label, str(value)))
    for label, value in fields:
        if "|" in value:
            raise ValueError("{}= contains '|' (field separator); reword it".format(label))
    memory_root = os.environ.get("OBU_MEMORY_DIR") or os.path.join(
        os.path.expanduser("~"), ".open-browser-use", "memory")
    os.makedirs(memory_root, exist_ok=True)
    lines_path = os.path.join(memory_root, "site-memory.lines")
    line = "|".join("{}={}".format(k, v) for k, v in fields)
    with open(lines_path, "a", encoding="utf-8") as fh:
        fh.write(line + "\n")
    return lines_path


def _help():
    """Print every helper with its one-line signature (full details via help())."""
    names = [
        "user_tabs",
        "tabs",
        "claim_tab",
        "open_tab",
        "navigate",
        "history",
        "name_session",
        "cdp",
        "evaluate",
        "wait_for_event",
        "memory_put",
        "close_tab",
        "finalize_tabs",
    ]
    print("obu helpers:")
    for name in names:
        fn = globals()[name]
        first_line = (fn.__doc__ or "").strip().splitlines()[0]
        print("  {:<14} {}".format(name, first_line))