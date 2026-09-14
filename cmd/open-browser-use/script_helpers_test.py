#!/usr/bin/env python3
"""Unit tests for script_helpers.py (obu heredoc prelude).

Run directly, or through scripts/ci.sh's unittest discovery, e.g.:
    python3 script_helpers_test.py

Spins up a fake Unix socket server that speaks the Browser Use JSON-RPC
frame protocol and asserts the helper call sequences and result shapes.
"""

import importlib.util
import json
import math
import os
from pathlib import Path
import socket
import struct
import sys
import tempfile
import threading
import time
import unittest
from unittest import mock

HERE = os.path.dirname(os.path.abspath(__file__))


def load_module():
    spec = importlib.util.spec_from_file_location(
        "script_helpers", os.path.join(HERE, "script_helpers.py")
    )
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class FakeHost(threading.Thread):
    """Accepts one connection, serves framed JSON-RPC, records requests."""

    def __init__(self, responses=None, errors=None):
        super().__init__(daemon=True)
        self.sock_path = tempfile.mktemp(prefix="obu-script-test-", suffix=".sock")
        self.responses = responses or {}
        self.errors = errors or set()
        # method -> list of extra JSON frames to send BEFORE that method's
        # response (notifications, mismatched ids) to exercise the prelude's
        # id correlation loop.
        self.extra_frames = {}
        self.requests = []
        self._conn = None
        self._ready = threading.Event()
        self.server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.server.bind(self.sock_path)
        self.server.listen(1)

    def run(self):
        self._ready.set()
        conn, _ = self.server.accept()
        self._conn = conn
        with conn:
            while True:
                header = self._read_exact(conn, 4)
                if not header:
                    return
                (length,) = struct.unpack("@I", header)
                body = json.loads(self._read_exact(conn, length).decode("utf-8"))
                self.requests.append(body)
                method = body["method"]
                for frame in self.extra_frames.get(method, []):
                    payload = json.dumps(frame).encode("utf-8")
                    conn.sendall(struct.pack("@I", len(payload)) + payload)
                if method in self.errors:
                    payload = json.dumps(
                        {
                            "jsonrpc": "2.0",
                            "id": body.get("id"),
                            "error": {"code": -32000, "message": "boom: " + method},
                        }
                    ).encode("utf-8")
                    conn.sendall(struct.pack("@I", len(payload)) + payload)
                    continue
                if method in self.responses:
                    result = self.responses[method]
                    # A tuple means "one response per request, in order".
                    # (Lists are legitimate responses, e.g. getUserTabs.)
                    if isinstance(result, tuple):
                        result, rest = (result[0], result[1:]) if result else ({"ok": True}, ())
                        if rest:
                            self.responses[method] = rest
                elif method == "getTabs":
                    result = []
                else:
                    result = {"ok": True}
                payload = json.dumps(
                    {"jsonrpc": "2.0", "id": body.get("id"), "result": result}
                ).encode("utf-8")
                conn.sendall(struct.pack("@I", len(payload)) + payload)

    @staticmethod
    def _read_exact(conn, count):
        chunks = []
        remaining = count
        while remaining > 0:
            chunk = conn.recv(remaining)
            if not chunk:
                return b""
            chunks.append(chunk)
            remaining -= len(chunk)
        return b"".join(chunks)

    def push(self, frame):
        """Send a frame to the attached client from outside the request loop
        (e.g. while the client is blocked in wait_for_event)."""
        if self._conn is None:
            raise RuntimeError("host connection not established yet")
        payload = json.dumps(frame).encode("utf-8")
        self._conn.sendall(struct.pack("@I", len(payload)) + payload)


class PreludeTest(unittest.TestCase):
    def setUp(self):
        self.host = FakeHost()
        self.host.start()
        self.host._ready.wait()
        os.environ["OBU_SCRIPT_SOCKET"] = self.host.sock_path
        os.environ["OBU_SESSION_ID"] = "test-session"
        self.sh = load_module()

    def tearDown(self):
        if self.sh._CONN is not None:
            self.sh._CONN.close()
        else:
            # Invalid-argument tests never connect; unblock the fake's accept.
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as conn:
                conn.connect(self.host.sock_path)
        self.host.join(1)
        self.host.server.close()
        try:
            os.unlink(self.host.sock_path)
        except OSError:
            pass

    def last(self, method):
        return next(r for r in reversed(self.host.requests) if r["method"] == method)

    def test_session_defaults_injected(self):
        self.sh.user_tabs()
        request = self.last("getUserTabs")
        self.assertEqual(request["params"]["session_id"], "test-session")
        self.assertTrue(request["params"]["turn_id"].startswith("test-session-"))

    def test_cdp_mode_defaults_to_extension(self):
        with mock.patch.dict(os.environ, {}, clear=True):
            self.sh.tabs()
            request = self.last("getTabs")
            self.assertEqual(request["params"].get("_cdpMode"), "extension")

    def test_cdp_mode_propagated_from_env(self):
        with mock.patch.dict(os.environ, {"OBU_CDP_MODE": "extension"}):
            self.sh.tabs()
            request = self.last("getTabs")
            self.assertEqual(request["params"].get("_cdpMode"), "extension")

    def test_direct_attach_starts_mac_approval_helper(self):
        proc = mock.Mock()
        proc.poll.return_value = None
        self.sh._MAC_APPROVAL_HELPER = "/tmp/open-browser-use"
        self.sh._MAC_APPROVAL_PROC = None
        with mock.patch.object(self.sh.sys, "platform", "darwin"), \
             mock.patch.object(self.sh.subprocess, "Popen", return_value=proc) as popen:
            self.sh._maybe_start_mac_remote_debugging_approval("attach", "direct")
        popen.assert_called_once()
        self.assertEqual(popen.call_args.args[0], ["/tmp/open-browser-use", "approve"])

    def test_extension_mode_never_starts_mac_approval_helper(self):
        self.sh._MAC_APPROVAL_HELPER = "/tmp/open-browser-use"
        self.sh._MAC_APPROVAL_PROC = None
        with mock.patch.object(self.sh.sys, "platform", "darwin"), \
             mock.patch.object(self.sh.subprocess, "Popen") as popen:
            self.sh._maybe_start_mac_remote_debugging_approval("attach", "extension")
        popen.assert_not_called()

    def test_request_starts_client_approval_after_direct_frame_is_sent(self):
        with mock.patch.object(self.sh, "_maybe_start_mac_remote_debugging_approval") as start:
            with mock.patch.dict(os.environ, {"OBU_CDP_MODE": "direct"}):
                self.sh._request("attach", {"tabId": 42})
        start.assert_called_once_with("attach", "direct")
        self.assertEqual(self.last("attach")["params"]["tabId"], 42)

    def test_request_includes_local_deadline_for_queue_expiry(self):
        orig_timeout = self.sh._TIMEOUT
        try:
            self.sh._TIMEOUT = 0.25
            before = int(time.time() * 1000)
            self.sh._request("getTabs", {"_deadlineUnixMs": 1})
            deadline = self.last("getTabs")["params"]["_deadlineUnixMs"]
            self.assertGreaterEqual(deadline, before + 240)
            self.assertLessEqual(deadline, int(time.time() * 1000) + 250)
        finally:
            self.sh._TIMEOUT = orig_timeout

    def test_user_tabs_returns_list(self):
        self.host.responses["getUserTabs"] = [{"id": 7, "url": "https://a"}]
        self.assertEqual(self.sh.user_tabs(), [{"id": 7, "url": "https://a"}])

    def test_claim_tab_sets_current_tab(self):
        self.sh.claim_tab(42)
        request = self.last("claimUserTab")
        self.assertEqual(request["params"]["tabId"], 42)
        # cdp after claim targets the claimed tab
        self.sh.cdp("DOM.getDocument")
        request = self.last("executeCdp")
        self.assertEqual(request["params"]["target"], {"tabId": 42})
        self.assertIn("attach", [r["method"] for r in self.host.requests])

    def test_claim_tab_tolerates_numeric_string(self):
        # Regression: claim_tab("279925170") must behave like claim_tab(279925170)
        # and send an int, so the extension never sees a string tabId.
        self.sh.claim_tab("279925170")
        request = self.last("claimUserTab")
        self.assertEqual(request["params"]["tabId"], 279925170)
        self.sh.cdp("DOM.getDocument")
        request = self.last("executeCdp")
        self.assertEqual(request["params"]["target"], {"tabId": 279925170})

    def test_claim_tab_tolerates_padded_numeric_string(self):
        self.sh.claim_tab("  42  ")
        request = self.last("claimUserTab")
        self.assertEqual(request["params"]["tabId"], 42)

    def test_claim_tab_rejects_non_numeric_string(self):
        with self.assertRaises(RuntimeError) as ctx:
            self.sh.claim_tab("abc")
        self.assertIn("claim_tab", str(ctx.exception))
        self.assertIn("integer", str(ctx.exception))
        # nothing was sent over the wire
        self.assertEqual([r["method"] for r in self.host.requests], [])

    def test_claim_tab_rejects_bool(self):
        with self.assertRaises(RuntimeError):
            self.sh.claim_tab(True)

    def test_claim_tab_rejects_nonpositive(self):
        with self.assertRaises(RuntimeError):
            self.sh.claim_tab(0)
        with self.assertRaises(RuntimeError):
            self.sh.claim_tab(-3)

    def test_navigate_tolerates_numeric_string(self):
        self.sh.navigate("7", "https://example.com")
        attach = self.last("attach")
        self.assertEqual(attach["params"]["tabId"], 7)
        request = self.last("executeCdp")
        self.assertEqual(request["params"]["method"], "Page.navigate")
        self.assertEqual(request["params"]["target"], {"tabId": 7})

    def test_navigate_rejects_non_numeric_string(self):
        with self.assertRaises(RuntimeError) as ctx:
            self.sh.navigate("abc", "https://example.com")
        self.assertIn("integer", str(ctx.exception))
        self.assertEqual([r["method"] for r in self.host.requests], [])

    def test_open_tab_with_url_sequence(self):
        self.host.responses["createTab"] = {"id": 9, "url": ""}
        self.sh.open_tab("https://example.com")
        methods = [r["method"] for r in self.host.requests]
        self.assertEqual(methods, ["createTab", "attach", "executeCdp"])
        navigate = [r for r in self.host.requests if r["method"] == "executeCdp"][-1]
        self.assertEqual(navigate["params"]["method"], "Page.navigate")
        self.assertEqual(
            navigate["params"]["commandParams"], {"url": "https://example.com"}
        )
        self.assertEqual(self.sh.open_tab(""), {"id": 9, "url": ""})

    def test_open_tab_cleans_up_on_navigation_failure(self):
        self.host.responses["createTab"] = {"id": 9, "url": ""}
        self.host.errors.add("executeCdp")
        with self.assertRaises(RuntimeError):
            self.sh.open_tab("https://example.com")
        methods = [r["method"] for r in self.host.requests]
        self.assertEqual(methods, ["createTab", "attach", "executeCdp", "closeUserTab"])
        close_req = [r for r in self.host.requests if r["method"] == "closeUserTab"][-1]
        self.assertEqual(close_req["params"]["tabId"], 9)

    def test_open_tab_window_mode_passthrough(self):
        self.host.responses["createTab"] = {"id": 9, "url": ""}
        # explicit override wins and reaches the wire
        self.sh.open_tab("", window_mode="user")
        # default leaves the key absent so the extension default (user) applies
        self.sh.open_tab()
        creates = [r for r in self.host.requests if r["method"] == "createTab"]
        self.assertEqual(len(creates), 2)
        self.assertEqual(creates[0]["params"]["window_mode"], "user")
        self.assertNotIn("window_mode", creates[1]["params"])

    def test_cdp_passes_kwargs_as_command_params(self):
        self.sh.claim_tab(1)
        self.sh.cdp("DOM.getBoxModel", backendNodeId=5, depth=1)
        request = self.last("executeCdp")
        self.assertEqual(request["params"]["method"], "DOM.getBoxModel")
        self.assertEqual(request["params"]["commandParams"], {"backendNodeId": 5, "depth": 1})

    def test_cdp_empty_session_errors(self):
        # No session tabs at all -> auto-resume has nothing to work on.
        with self.assertRaises(RuntimeError) as ctx:
            self.sh.cdp("DOM.getDocument")
        self.assertIn("no current tab", str(ctx.exception))
        self.assertIn("no tabs", str(ctx.exception))

    def test_cdp_resumes_session_active_tab_when_unclaimed(self):
        # A fresh process with no claim resumes the session's logical active
        # tab reported by the extension (persisted across runs).
        self.host.responses["getTabs"] = [
            {"id": 5, "url": "https://x.com", "active": True},
            {"id": 6, "url": "https://example.com", "active": False},
        ]
        self.sh.cdp("DOM.getDocument")
        request = self.last("executeCdp")
        self.assertEqual(request["params"]["target"]["tabId"], 5)
        # Auto-resume consults session tabs only, never the user's tabs.
        self.assertIsNone(
            next((r for r in self.host.requests if r["method"] == "getUserTabs"), None)
        )

    def test_evaluate_resumes_session_active_tab(self):
        self.host.responses["getTabs"] = [
            {"id": 9, "url": "https://x.com", "active": True},
            {"id": 10, "url": "https://example.com", "active": False},
        ]
        self.host.responses["executeCdp"] = {"result": {"type": "string", "value": "ok"}}
        self.assertEqual(self.sh.evaluate("document.title"), "ok")
        request = self.last("executeCdp")
        self.assertEqual(request["params"]["target"]["tabId"], 9)

    def test_explicit_claim_overrides_session_active(self):
        # An explicit pick beats the resumed default even when another tab
        # is reported active.
        self.host.responses["getTabs"] = [
            {"id": 5, "url": "https://x.com", "active": True},
            {"id": 6, "url": "https://example.com", "active": False},
        ]
        self.sh.claim_tab(6)
        self.sh.cdp("DOM.getDocument")
        request = self.last("executeCdp")
        self.assertEqual(request["params"]["target"]["tabId"], 6)

    def test_evaluate_unwraps_remote_object(self):
        self.host.responses["executeCdp"] = {
            "result": {"type": "string", "value": "hello"}
        }
        self.sh.claim_tab(1)
        self.assertEqual(self.sh.evaluate("document.title"), "hello")
        request = self.last("executeCdp")
        self.assertEqual(
            request["params"]["commandParams"],
            {
                "expression": "document.title",
                "returnByValue": True,
                "awaitPromise": True,
            },
        )

    def test_evaluate_retries_top_level_return_wrapped(self):
        # First attempt fails with Chrome's illegal-return error; the second
        # is the same snippet wrapped in (function(){...})(), mirroring
        # browser-harness js(): both bare expressions and return statements
        # work.
        self.host.responses["executeCdp"] = (
            {
                "result": {"type": "object"},
                "exceptionDetails": {
                    "text": "Illegal return statement",
                    "lineNumber": 1,
                    "columnNumber": 8,
                },
            },
            {"result": {"type": "number", "value": 42}},
        )
        self.sh.claim_tab(1)
        self.assertEqual(self.sh.evaluate("const x = 42; return x"), 42)
        calls = [r for r in self.host.requests if r["method"] == "executeCdp"]
        self.assertEqual(len(calls), 2)
        self.assertEqual(
            calls[0]["params"]["commandParams"]["expression"], "const x = 42; return x"
        )
        self.assertEqual(
            calls[1]["params"]["commandParams"]["expression"],
            "(function(){const x = 42; return x})()",
        )

    def test_evaluate_nested_return_not_rewrapped(self):
        # A return inside a function body is legal as-is: one attempt only.
        self.host.responses["executeCdp"] = {
            "result": {"type": "number", "value": 6}
        }
        self.sh.claim_tab(1)
        expr = "[1, 2, 3].reduce((a, b) => { return a + b }, 0)"
        self.assertEqual(self.sh.evaluate(expr), 6)
        expression = [
            r for r in self.host.requests if r["method"] == "executeCdp"
        ][0]["params"]["commandParams"]["expression"]
        self.assertEqual(expression, expr)

    def test_evaluate_bare_arrow_function_returns_none_unexecuted(self):
        # A bare "() => {...}" is a valid expression whose value is a function
        # object. returnByValue cannot serialize functions, so CDP returns a
        # result with no value and no exception: the function never runs and
        # evaluate() silently yields None. This is the #1 LLM footgun — there
        # is no auto-invocation; call the function inside the snippet instead.
        self.host.responses["executeCdp"] = {
            "result": {
                "type": "function",
                "className": "Function",
                "description": "() => ({a: 1})",
            }
        }
        self.sh.claim_tab(1)
        self.assertIsNone(self.sh.evaluate("() => ({a: 1})"))
        calls = [r for r in self.host.requests if r["method"] == "executeCdp"]
        self.assertEqual(len(calls), 1)  # no illegal-return retry wrap kicked in
        self.assertEqual(
            calls[0]["params"]["commandParams"]["expression"], "() => ({a: 1})"
        )

    def test_evaluate_iife_arrow_function_executes(self):
        # The same body wrapped in an IIFE runs and returns its value; the
        # async IIFE form resolves through awaitPromise as well.
        self.host.responses["executeCdp"] = {"result": {"type": "object", "value": {"a": 1}}}
        self.sh.claim_tab(1)
        self.assertEqual(self.sh.evaluate("(() => ({a: 1}))()"), {"a": 1})
        self.host.responses["executeCdp"] = {"result": {"type": "number", "value": 42}}
        self.assertEqual(self.sh.evaluate("(async () => 42)()"), 42)

    def test_evaluate_decodes_unserializable_values(self):
        self.host.responses["executeCdp"] = {
            "result": {"type": "bigint", "unserializableValue": "9007199254740993n"}
        }
        self.sh.claim_tab(1)
        self.assertEqual(self.sh.evaluate("9007199254740993n"), 9007199254740993)
        self.host.responses["executeCdp"] = {
            "result": {"type": "number", "unserializableValue": "-Infinity"}
        }
        self.assertEqual(self.sh.evaluate("-Infinity"), -math.inf)

    def test_evaluate_error_reports_description_and_location(self):
        self.host.responses["executeCdp"] = {
            "result": {"type": "object"},
            "exceptionDetails": {
                "text": "Uncaught",
                "lineNumber": 1,
                "columnNumber": 3,
                "exception": {"description": "TypeError: boom is not a function"},
            },
        }
        self.sh.claim_tab(1)
        with self.assertRaises(RuntimeError) as ctx:
            self.sh.evaluate("boom()")
        message = str(ctx.exception)
        self.assertIn("evaluate threw", message)
        self.assertIn("TypeError: boom is not a function", message)
        self.assertIn("at line 1, column 3", message)

    def test_evaluate_raises_on_exception(self):
        self.host.responses["executeCdp"] = {
            "result": {"type": "object"},
            "exceptionDetails": {"text": "ReferenceError: nope"},
        }
        self.sh.claim_tab(1)
        with self.assertRaises(RuntimeError) as ctx:
            self.sh.evaluate("nope()")
        self.assertIn("evaluate threw", str(ctx.exception))

    def test_release_and_deliverable_are_separate(self):
        self.sh.claim_tab(3)
        self.sh.mark_deliverable("3")
        self.assertEqual(self.last("markDeliverable")["params"]["tabId"], 3)
        self.assertEqual(self.sh._CURRENT_TAB, 3)
        self.host.responses["releaseTabs"] = {"released": [3], "alreadyReleased": [], "failed": [], "remaining": []}
        self.sh.release_tabs()
        self.assertNotIn("tabIds", self.last("releaseTabs")["params"])
        self.assertIsNone(self.sh._CURRENT_TAB)
        self.sh.release_tabs([])
        self.assertEqual(self.last("releaseTabs")["params"]["tabIds"], [])
        self.sh.release_tabs(["3", 4])
        self.assertEqual(self.last("releaseTabs")["params"]["tabIds"], [3, 4])

    def test_finalize_tabs_defaults_empty_keep(self):
        self.sh.finalize_tabs()
        request = self.last("finalizeTabs")
        self.assertEqual(request["params"]["keep"], [])
        self.sh.finalize_tabs(keep=[{"tabId": 3, "status": "deliverable"}])
        request = self.last("finalizeTabs")
        self.assertEqual(request["params"]["keep"], [{"tabId": 3, "status": "deliverable"}])

    def test_rpc_error_becomes_runtime_error(self):
        self.host.errors.add("getUserHistory")
        with self.assertRaises(RuntimeError) as ctx:
            self.sh.history("x", limit=5)
        self.assertIn("boom: getUserHistory", str(ctx.exception))
        self.assertIn("getUserHistory", [r["method"] for r in self.host.requests])

    def test_requests_use_unique_string_ids(self):
        self.sh.user_tabs()
        self.sh.tabs()
        ids = [r["id"] for r in self.host.requests]
        self.assertEqual(len(ids), len(set(ids)), "request ids must be unique")
        self.assertTrue(
            all(isinstance(i, str) and i.startswith("obu-script-") for i in ids),
            "request ids must stay strings so the relay can correlate them",
        )

    def test_skips_notification_frames_before_response(self):
        # onCDPEvent/heartbeat are id-less notifications the relay broadcasts;
        # previously the prelude misread them as the response and returned None.
        self.host.responses["getUserTabs"] = [{"id": 7, "url": "https://a"}]
        self.host.extra_frames["getUserTabs"] = [
            {
                "jsonrpc": "2.0",
                "method": "onCDPEvent",
                "params": {
                    "source": {"tabId": 7},
                    "method": "Page.frameNavigated",
                    "params": {},
                },
            },
            {"jsonrpc": "2.0", "method": "heartbeat", "params": {"at": "2026-08-24T00:00:00Z"}},
        ]
        self.assertEqual(self.sh.user_tabs(), [{"id": 7, "url": "https://a"}])

    def test_skips_nonmatching_response_id(self):
        # A response frame whose id does not match this request must be
        # skipped in favor of the real response, not returned as the result.
        self.host.responses["getUserTabs"] = [{"id": 7, "url": "https://a"}]
        self.host.extra_frames["getUserTabs"] = [
            {"jsonrpc": "2.0", "id": "obu-script-999", "result": {"stale": True}}
        ]
        self.assertEqual(self.sh.user_tabs(), [{"id": 7, "url": "https://a"}])

    def test_cdp_unwraps_ax_tree_after_interleaved_events(self):
        # Regression for the x.com scenario: open_tab(navigate) -> events
        # queued -> cdp(Accessibility.getFullAXTree) must still return nodes.
        self.host.responses["createTab"] = {"id": 9, "url": ""}
        self.host.responses["executeCdp"] = {
            "nodes": [{"role": {"value": "heading"}, "name": {"value": "Home"}}]
        }
        self.host.extra_frames["executeCdp"] = [
            {
                "jsonrpc": "2.0",
                "method": "onCDPEvent",
                "params": {"source": {"tabId": 9}, "method": "Page.loadEventFired", "params": {}},
            }
        ]
        self.sh.open_tab("https://x.com/home")
        tree = self.sh.cdp("Accessibility.getFullAXTree")["nodes"]
        self.assertEqual(tree[0]["role"]["value"], "heading")

    def _on_cdp_event(self, inner_method, inner_params=None, tab_id=7):
        return {
            "jsonrpc": "2.0",
            "method": "onCDPEvent",
            "params": {
                "source": {"tabId": tab_id},
                "method": inner_method,
                "params": inner_params or {},
            },
        }

    def test_wait_for_event_consumes_queued_notifications(self):
        # Events that arrived while another helper's response was being read
        # must not be lost: _request queues them, wait_for_event drains them.
        self.host.responses["getUserTabs"] = [{"id": 7, "url": "https://a"}]
        self.host.extra_frames["getUserTabs"] = [
            self._on_cdp_event(
                "Network.responseReceived",
                {"response": {"url": "https://x.com/i/api/graphql/x/HomeTimeline", "status": 200}},
            )
        ]
        self.sh.user_tabs()
        ev = self.sh.wait_for_event("Network.responseReceived", timeout=5)
        self.assertEqual(ev["response"]["status"], 200)
        self.assertIn("HomeTimeline", ev["response"]["url"])

    def test_wait_for_event_predicate_filters_queued_events(self):
        # Non-matching events are counted (and visible in the timeout message)
        # but skipped; a matching later event wins.
        self.host.responses["getUserTabs"] = [{"id": 7, "url": "https://a"}]
        self.host.extra_frames["getUserTabs"] = [
            self._on_cdp_event(
                "Network.responseReceived",
                {"response": {"url": "https://other.example/a", "status": 404}},
            ),
            self._on_cdp_event(
                "Network.responseReceived",
                {"response": {"url": "https://x.com/i/api/graphql/y/HomeTimeline", "status": 200}},
            ),
        ]
        self.sh.user_tabs()
        ev = self.sh.wait_for_event(
            "Network.responseReceived",
            predicate=lambda e: "HomeTimeline" in e["params"]["response"]["url"],
            timeout=5,
        )
        self.assertEqual(ev["response"]["status"], 200)

    def test_wait_for_event_picks_up_event_while_waiting(self):
        # The common shape: trigger first, then block; the event arrives after
        # the helper started reading the socket.
        self.sh.user_tabs()  # establishes the connection before the waiter runs
        result = {}

        def waiter():
            result["ev"] = self.sh.wait_for_event("Page.loadEventFired", timeout=10)

        t = threading.Thread(target=waiter)
        t.start()
        time.sleep(0.2)
        self.host.push(self._on_cdp_event("Page.loadEventFired", {"ts": 1}))
        t.join(10)
        self.assertIn("ev", result)
        self.assertEqual(result["ev"]["ts"], 1)

    def test_wait_for_event_timeout_reports_diagnostics(self):
        # A timeout must say what was seen (and hint at Network.enable when no
        # Network.* events ever arrived) so the caller can fix the setup.
        self.sh.user_tabs()
        self.host.push(self._on_cdp_event("Log.entryAdded", {"entry": {"level": "error"}}))
        self.host.push({"jsonrpc": "2.0", "method": "heartbeat", "params": {"at": "x"}})
        with self.assertRaises(RuntimeError) as ctx:
            self.sh.wait_for_event("Network.responseReceived", timeout=0.3)
        msg = str(ctx.exception)
        self.assertIn("Network.responseReceived", msg)
        self.assertIn("timed out", msg)
        self.assertIn("Log.entryAdded", msg)  # diagnostic lists what was seen
        self.assertIn('Network.enable', msg)  # missing-domain hint for Network.*

    def test_wait_for_event_timeout_lists_response_urls(self):
        # When Network.responseReceived events flow but none pass the predicate,
        # the timeout diagnostic must name their URLs — otherwise a too-narrow
        # predicate (e.g. matching the JS bundle instead of the GraphQL endpoint)
        # looks exactly like "no events at all".
        self.sh.user_tabs()
        self.host.push(self._on_cdp_event(
            "Network.responseReceived",
            {"response": {"url": "https://x.com/shared~HomeTimeline.bundle.js", "status": 200}},
        ))
        self.host.push(self._on_cdp_event(
            "Network.responseReceived",
            {"response": {"url": "https://x.com/i/api/graphql/wp06oo3fR/HomeTimeline?count=20", "status": 200}},
        ))
        with self.assertRaises(RuntimeError) as ctx:
            self.sh.wait_for_event(
                "Network.responseReceived",
                predicate=lambda e: "variables=%7B%22cursor" in e["params"]["response"]["url"],
                timeout=0.3,
            )
        msg = str(ctx.exception)
        self.assertIn("HomeTimeline.bundle.js", msg)
        self.assertIn("HomeTimeline", msg)
        self.assertNotIn("count=20", msg)
        self.assertIn("Network.responseReceived URLs", msg)

    def test_wait_for_event_rejects_bad_args(self):
        with self.assertRaises(ValueError):
            self.sh.wait_for_event("", timeout=5)
        with self.assertRaises(ValueError):
            self.sh.wait_for_event("Network.responseReceived", timeout=0)

    def test_wait_for_event_predicate_raise_gets_reported(self):
        self.sh.user_tabs()
        self.host.push(self._on_cdp_event("Page.loadEventFired"))
        with self.assertRaises(RuntimeError) as ctx:
            self.sh.wait_for_event(
                "Page.loadEventFired",
                predicate=lambda e: e["params"]["missing"],
                timeout=5,
            )
        self.assertIn("predicate raised", str(ctx.exception))

    def test_help_lists_helpers(self):
        self.sh._help()

    def test_help_accepts_helper_name_strings(self):
        import contextlib
        import io
        buf = io.StringIO()
        with contextlib.redirect_stdout(buf):
            self.sh.help("cdp")
        out = buf.getvalue()
        self.assertIn("cdp(", out)
        self.assertNotIn("No Python documentation", out)
        # object form keeps working
        buf2 = io.StringIO()
        with contextlib.redirect_stdout(buf2):
            self.sh.help(self.sh.finalize_tabs)
        self.assertIn("finalize_tabs(", buf2.getvalue())

    def test_help_unknown_string_falls_back_to_builtin(self):
        import contextlib
        import io
        buf = io.StringIO()
        with contextlib.redirect_stdout(buf):
            self.sh.help("no_such_helper_xyz")
        self.assertIn("No Python documentation found", buf.getvalue())
        self.assertTrue(self.sh.open_tab.__doc__.startswith("Open a new session tab"))
        self.assertIn("wait_for_event", {
            "user_tabs", "tabs", "claim_tab", "open_tab", "navigate",
            "history", "name_session", "cdp", "evaluate", "wait_for_event",
            "memory_put", "close_tab", "release_tabs", "mark_deliverable",
        })

    def test_memory_put_accepts_subject_body_aliases(self):
        # Agents guess the kwarg names (subject=/body= instead of note=); map
        # them instead of failing with a bare TypeError.
        temp = tempfile.TemporaryDirectory(prefix="obu-mem-test-")
        self.addCleanup(temp.cleanup)
        tmp = temp.name
        old = os.environ.get("OBU_MEMORY_DIR")
        os.environ["OBU_MEMORY_DIR"] = tmp
        try:
            path = self.sh.memory_put("x.com", kind="tip", subject="s", body="b")
            line = Path(path).read_text(encoding="utf-8").strip()
            self.assertIn("domain=x.com", line)
            self.assertIn("note=s b", line)
            path2 = self.sh.memory_put("x.com", kind="fail", note="n", how="h")
            line2 = Path(path2).read_text(encoding="utf-8").strip()
            self.assertIn("kind=fail", line2)
            self.assertIn("unlock=h", line2)
        finally:
            if old is None:
                os.environ.pop("OBU_MEMORY_DIR", None)
            else:
                os.environ["OBU_MEMORY_DIR"] = old

    def test_memory_put_unknown_kwarg_names_valid_fields(self):
        with self.assertRaises(TypeError) as ctx:
            self.sh.memory_put("x.com", kind="tip", note="n", subject2="x")
        msg = str(ctx.exception)
        self.assertIn("subject2", msg)
        self.assertIn("note / unlock / selector / tech / runbook", msg)

    def test_memory_put_kind_runbook_hints_at_field(self):
        with self.assertRaises(ValueError) as ctx:
            self.sh.memory_put("x.com", kind="runbook", note="n")
        self.assertIn("runbook is a field", str(ctx.exception))


if __name__ == "__main__":
    unittest.main(verbosity=2)
