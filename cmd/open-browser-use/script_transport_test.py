"""Deterministic transport regressions; no Chrome or native host required."""

import json
import math
import socket
import struct
import threading
import time
import unittest
from unittest import mock

from script_helpers_test import load_module


def event(method, **params):
    return {"method": "onCDPEvent", "params": {
        "source": {"tabId": 7}, "method": method, "params": params,
    }}


def frame(value):
    payload = json.dumps(value).encode()
    return struct.pack("@I", len(payload)) + payload


class TransportTest(unittest.TestCase):
    def setUp(self):
        self.sh = load_module()
        self.client, self.peer = socket.socketpair()
        self.client.settimeout(0.8)
        self.peer.settimeout(1)
        self.sh._CONN = self.client
        self.sh._SOCKET_PATH = "synthetic"
        self.sh._SOCKET_ERROR = ""
        self.sh._TIMEOUT = 0.1
        self.addCleanup(self.client.close)
        self.addCleanup(self.peer.close)

    def test_wait_retains_other_methods_and_predicate_misses(self):
        events = [event("Network.loadingFinished", requestId="a"),
                  event("Network.responseReceived", requestId="b"),
                  event("Network.responseReceived", requestId="a")]
        self.sh._EVENT_QUEUE.extend(events)
        got = self.sh.wait_for_event("Network.responseReceived",
                                    lambda e: e["params"]["requestId"] == "a")
        self.assertEqual(got, {"requestId": "a"})
        self.assertEqual(list(self.sh._EVENT_QUEUE), events[:2])
        self.assertEqual(self.sh.wait_for_event("Network.loadingFinished"), {"requestId": "a"})
        self.assertEqual(self.sh.wait_for_event("Network.responseReceived"), {"requestId": "b"})

    def test_live_unmatched_events_survive_a_timeout(self):
        self.peer.sendall(frame(event("Network.loadingFinished", requestId="a")))
        with self.assertRaisesRegex(RuntimeError, "timed out"):
            self.sh.wait_for_event("Page.loadEventFired", timeout=0.02)
        self.assertEqual(self.sh.wait_for_event("Network.loadingFinished", timeout=0.05), {"requestId": "a"})
        self.assertEqual(self.client.gettimeout(), 0.8)

    def test_predicate_exception_restores_timeout_and_preserves_event(self):
        self.peer.sendall(frame(event("Page.loadEventFired", timestamp=1)))
        with self.assertRaisesRegex(RuntimeError, "predicate raised"):
            self.sh.wait_for_event("Page.loadEventFired", lambda e: e["missing"], timeout=0.02)
        self.assertEqual(self.client.gettimeout(), 0.8)
        self.assertEqual(self.sh.wait_for_event("Page.loadEventFired"), {"timestamp": 1})

    def test_eof_restores_previous_timeout(self):
        self.peer.close()
        with self.assertRaisesRegex(RuntimeError, "closed"):
            self.sh.wait_for_event("Page.loadEventFired", timeout=0.02)
        self.assertEqual(self.client.gettimeout(), 0.8)

    def test_partial_event_frame_survives_wait_timeout(self):
        data = frame(event("Page.loadEventFired", timestamp=1))
        for cut in (2, 8):
            with self.subTest(cut=cut):
                self.peer.sendall(data[:cut])
                with self.assertRaisesRegex(RuntimeError, "timed out"):
                    self.sh.wait_for_event("Page.loadEventFired", timeout=0.02)
                self.peer.sendall(data[cut:])
                self.assertEqual(self.sh.wait_for_event("Page.loadEventFired", timeout=0.05),
                                 {"timestamp": 1})

    def test_partial_event_can_be_completed_by_next_request(self):
        data = frame(event("Page.loadEventFired", timestamp=1))
        self.peer.sendall(data[:8])
        with self.assertRaisesRegex(RuntimeError, "timed out"):
            self.sh.wait_for_event("Page.loadEventFired", timeout=0.02)
        self.peer.sendall(data[8:] + frame({"id": "obu-script-1", "result": "pong"}))
        self.assertEqual(self.sh._request("ping"), "pong")
        self.assertEqual(self.sh.wait_for_event("Page.loadEventFired"), {"timestamp": 1})

    def test_partial_response_survives_request_timeout_without_replaying(self):
        data = frame({"id": "obu-script-1", "result": "late"})
        self.peer.sendall(data[:8])
        with self.assertRaises(socket.timeout):
            self.sh._request("syntheticAction")
        self.peer.sendall(data[8:] + frame({"id": "obu-script-2", "result": "next"}))
        self.assertEqual(self.sh._request("ping"), "next")
        self.assertEqual(self.sh._NEXT_REQ_ID, 2)
        self.assertEqual(self.client.gettimeout(), 0.8)

    def test_request_deadline_is_not_extended_by_event_traffic(self):
        def producer():
            for _ in range(30):
                try:
                    self.peer.sendall(frame(event("Network.dataReceived")))
                except OSError:
                    return
                time.sleep(0.01)
        thread = threading.Thread(target=producer)
        thread.start()
        start = time.monotonic()
        try:
            with self.assertRaises(socket.timeout):
                self.sh._request("ping")
            self.assertLess(time.monotonic() - start, 0.25)
        finally:
            thread.join(1)

    def test_wait_rejects_nonfinite_deadlines_before_io(self):
        for timeout in (0, -1, math.nan, math.inf):
            with self.subTest(timeout=timeout), self.assertRaises(ValueError):
                self.sh.wait_for_event("Page.loadEventFired", timeout=timeout)

    def test_response_ids_zero_and_null_are_not_notifications(self):
        self.peer.sendall(frame({"id": 0, "result": "stray"}) +
                          frame({"id": None, "error": {}}) +
                          frame({"id": "obu-script-1", "result": "pong"}))
        self.assertEqual(self.sh._request("ping"), "pong")
        self.assertEqual(list(self.sh._EVENT_QUEUE), [])

    def test_oversized_frame_fails_before_reading_body(self):
        self.peer.sendall(struct.pack("@I", 64 * 1024 * 1024 + 1))
        with self.assertRaisesRegex(RuntimeError, "frame too large"):
            self.sh._request("ping")

    def test_queue_stays_bounded(self):
        self.sh._TIMEOUT = 1.0
        self.peer.settimeout(2.0)
        events = [event("Network.dataReceived", index=i) for i in range(510)]
        payload = b"".join(frame(e) for e in events) + frame({"id": "obu-script-1", "result": "pong"})
        sender = threading.Thread(target=self.peer.sendall, args=(payload,))
        sender.start()
        try:
            self.assertEqual(self.sh._request("ping"), "pong")
        finally:
            sender.join(1)
        self.assertEqual(len(self.sh._EVENT_QUEUE), 500)
        self.assertEqual(self.sh._EVENT_QUEUE[0]["params"]["params"]["index"], 10)
        # An immediately consumed live match must not evict retained events.
        self.peer.sendall(frame(event("Page.loadEventFired", timestamp=1)))
        self.sh.wait_for_event("Page.loadEventFired", timeout=0.1)
        self.assertEqual(list(self.sh._EVENT_QUEUE), events[-500:])

    def test_timeout_url_diagnostics_redact_credentials_query_and_fragment(self):
        self.sh._EVENT_QUEUE.append(event("Network.responseReceived", response={
            "url": "https://user:secret@example.test/api/items?token=private#private-fragment"}))
        with self.assertRaises(RuntimeError) as ctx:
            self.sh.wait_for_event("Network.responseReceived", lambda _: False, timeout=0.02)
        message = str(ctx.exception)
        self.assertIn("https://example.test/api/items", message)
        for secret in ("user:", "secret", "token=", "private"):
            self.assertNotIn(secret, message)

    def test_failed_send_invalidates_connection_without_replay(self):
        candidate = mock.Mock()
        candidate.sendall.side_effect = socket.timeout("synthetic partial write")
        self.sh._CONN = candidate
        with self.assertRaises(socket.timeout):
            self.sh._request("syntheticAction")
        candidate.close.assert_called_once()
        with self.assertRaisesRegex(RuntimeError, "delivery is unknown"):
            self.sh._request("syntheticAction")
        candidate.sendall.assert_called_once()

    def test_api_sheet_works_without_sigpipe(self):
        import contextlib
        import io
        import signal
        out = io.StringIO()
        with mock.patch.dict(signal.__dict__):
            signal.__dict__.pop("SIGPIPE", None)
            with contextlib.redirect_stdout(out):
                self.sh._api_sheet()
        self.assertIn("wait_for_event", out.getvalue())

    def test_failed_connect_does_not_cache_a_broken_socket(self):
        self.sh._CONN = None
        candidate = mock.Mock()
        candidate.connect.side_effect = OSError("synthetic failure")
        with mock.patch.object(self.sh.socket, "socket", return_value=candidate):
            with self.assertRaisesRegex(RuntimeError, "cannot connect"):
                self.sh._get_conn()
        candidate.close.assert_called_once()
        self.assertIsNone(self.sh._CONN)


if __name__ == "__main__":
    unittest.main(verbosity=2)
