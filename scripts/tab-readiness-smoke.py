"""Run: obu exec /absolute/path/tab-readiness-smoke.py

Requires the updated extension and an explicitly selected target. Creates only
synthetic about:blank pages, freezes its own fixture with CDP, and verifies that
ordinary helpers recover it without an agent-side activation/reload ritual.
No existing user page is claimed or altered. All fixture tabs are closed.
"""
import time

info = _request("getInfo")
assert info["metadata"].get("tabReadiness") == "bounded-v1", "Reload the updated OBU extension first"
known = {tab["id"] for tab in user_tabs()}
page = open_tab(window_mode="user")
tab_id = page["id"]
assert tab_id not in known
try:
    evaluate("document.title = 'OBU readiness smoke'; document.body.innerHTML = '<h1>Readiness fixture</h1>'; window.fixtureMarker = 42")
    cdp("Accessibility.getFullAXTree")
    cdp("Page.setWebLifecycleState", state="frozen")
    started = time.monotonic()
    # This ordinary helper must wake the renderer internally. No activate,
    # reload, window focus, or lifecycle command from the caller for recovery.
    assert evaluate("window.fixtureMarker") == 42, "resident state must survive (no reload)"
    assert time.monotonic() - started < 8
    assert not release_tabs()["remaining"]
    claim_tab(tab_id)
    mark_deliverable(tab_id)
    assert evaluate("document.title") == "OBU readiness smoke"
    # Verify problem 1: deliverable moved to completion group, and no new Task - OBU tab leaked
    user_pages = user_tabs()
    our_page = next((t for t in user_pages if t["id"] == tab_id), None)
    assert our_page is not None
    assert our_page.get("tabGroup") == "✅ Open Browser Use", f"expected deliverable group, got {our_page.get('tabGroup')}"
    new_task_group_tabs = [
        t for t in user_pages
        if t["id"] not in known and t["id"] != tab_id and t.get("tabGroup") == "Task - OBU"
    ]
    assert not new_task_group_tabs, f"smoke leaked new tabs in Task - OBU: {new_task_group_tabs}"
    assert not release_tabs()["remaining"]
    print("PASS: frozen fixture recovered internally; state preserved; deliverable moved; release/reclaim/mark works")
finally:
    claim_tab(tab_id)
    close_tab(tab_id)
    report = release_tabs()
    assert not report["failed"] and not report["remaining"], report
    print("PASS: all synthetic fixture tabs closed and ownership released")
