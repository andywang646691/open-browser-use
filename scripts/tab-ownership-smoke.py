"""Run with: obu exec /absolute/path/tab-ownership-smoke.py

Requires the updated extension already loaded and one explicitly selected target.
Creates only a synthetic page; no existing user page is claimed or changed.
Use --session-id to choose an isolated test identity. All created pages are closed.
"""

# Probe the new method before creating any page. An old extension must fail here.
assert not release_tabs([])["failed"]
known = {tab["id"] for tab in user_tabs()}
page = open_tab(window_mode="isolated")
tab_id = page["id"]
assert tab_id not in known
try:
    # Populate only our fresh about:blank fixture; avoid data-URL navigation
    # restrictions and asynchronous navigation unrelated to ownership.
    evaluate("document.title = 'OBU ownership smoke'; document.body.innerHTML = '<h1>Ownership fixture</h1>'")
    cdp("Accessibility.getFullAXTree")
    assert evaluate("document.title") == "OBU ownership smoke"
    result = release_tabs([tab_id])
    assert not result["failed"] and tab_id in result["released"], result
    assert not any(tab["id"] == tab_id for tab in tabs())
    assert any(tab["id"] == tab_id and tab["ownerSessionId"] is None for tab in user_tabs())
    claim_tab(tab_id)
    mark_deliverable(tab_id)
    assert any(tab["id"] == tab_id for tab in tabs())
    assert evaluate("document.title") == "OBU ownership smoke"
    print("PASS: release preserves page, rediscovery/reclaim works, deliverable retains ownership")
finally:
    # Only the freshly created fixture is eligible for cleanup.
    claim_tab(tab_id)
    close_tab(tab_id)
    report = release_tabs()
    assert not report["failed"] and not report["remaining"], report
