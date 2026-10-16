"""End-to-end tests against a tiny in-process fake Videohub."""

import json
import socket
import tempfile
import threading
import time
import unittest
import urllib.request
from pathlib import Path

from videohub_control.client import CommandError, VideohubClient
from videohub_control.server import make_server
from videohub_control.store import Store
from tests.test_protocol import PRELUDE


class FakeHub:
    """Sends the prelude, ACKs commands, echoes routing/label/lock changes."""

    def __init__(self, nak_labels: bool = False) -> None:
        self.srv = socket.create_server(("127.0.0.1", 0))
        self.port = self.srv.getsockname()[1]
        self.received: list[str] = []
        self.nak_labels = nak_labels
        threading.Thread(target=self._serve, daemon=True).start()

    def _serve(self) -> None:
        conn, _ = self.srv.accept()
        conn.sendall(PRELUDE.encode())
        buf = ""
        while True:
            data = conn.recv(4096)
            if not data:
                return
            buf += data.decode()
            while "\n\n" in buf:
                block, buf = buf.split("\n\n", 1)
                self.received.append(block)
                header = block.split("\n")[0]
                if header == "INPUT LABELS:" and self.nak_labels:
                    conn.sendall(b"NAK\n\n")
                    continue
                conn.sendall(b"ACK\n\n")
                if header != "PING:":
                    conn.sendall((block + "\n\n").encode())

    def close(self) -> None:
        self.srv.close()


def wait_for(cond, timeout=3.0):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        if cond():
            return True
        time.sleep(0.02)
    return False


class ClientTests(unittest.TestCase):
    def setUp(self):
        self.hub = FakeHub(nak_labels=True)
        self.client = VideohubClient("127.0.0.1", self.hub.port)
        self.client.start()
        self.assertTrue(wait_for(lambda: self.client.status == "connected"))

    def tearDown(self):
        self.client.stop()
        self.hub.close()

    def test_route_updates_state(self):
        self.client.route("video", [(0, 3), (2, 1)])
        self.assertIn("VIDEO OUTPUT ROUTING:\n0 3\n2 1", self.hub.received)
        self.assertTrue(wait_for(lambda: self.client.state.routing == [3, 1, 1]))

    def test_monitor_route_and_locks(self):
        self.client.route("monitor", [(0, 1)])
        self.client.set_locks("video", [(0, "O")])
        self.assertTrue(wait_for(lambda: self.client.state.monitor_routing == [1] and self.client.state.locks[0] == "O"))

    def test_nak_raises(self):
        with self.assertRaises(CommandError):
            self.client.set_labels("input", [(0, "X")])

    def test_invalid_lock_rejected(self):
        with self.assertRaises(CommandError):
            self.client.set_locks("video", [(0, "Z")])


class ServerTests(unittest.TestCase):
    def setUp(self):
        self.hub = FakeHub()
        self.tmp = tempfile.TemporaryDirectory()
        self.httpd, self.ctl = make_server("127.0.0.1", 0, Store(Path(self.tmp.name) / "c.json"))
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()
        self.base = f"http://127.0.0.1:{self.httpd.server_address[1]}"

    def tearDown(self):
        if self.ctl.client:
            self.ctl.client.stop()
        self.httpd.shutdown()
        self.httpd.server_close()
        self.hub.close()
        self.tmp.cleanup()

    def post(self, path, body):
        req = urllib.request.Request(self.base + path, json.dumps(body).encode(),
                                     {"Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req) as r:
                return r.status, json.loads(r.read())
        except urllib.error.HTTPError as e:
            return e.code, json.loads(e.read())

    def state(self):
        with urllib.request.urlopen(self.base + "/api/state") as r:
            return json.loads(r.read())

    def test_full_flow(self):
        self.assertEqual(self.post("/api/route", {"routes": [[0, 1]]})[0], 409)
        self.assertEqual(self.post("/api/connect", {"host": "127.0.0.1", "port": self.hub.port})[0], 200)
        self.assertTrue(wait_for(lambda: (self.state()["connection"] or {}).get("status") == "connected"))

        self.assertEqual(self.post("/api/route", {"kind": "video", "routes": [[1, 3]]})[0], 200)
        self.assertTrue(wait_for(lambda: self.state()["connection"]["hub"]["routing"] == [2, 3, 0]))

        self.assertEqual(self.post("/api/labels", {"kind": "output", "labels": [[0, "Programme"]]})[0], 200)
        self.assertTrue(wait_for(lambda: self.state()["connection"]["hub"]["outputLabels"][0] == "Programme"))

        # Salvo: save, change routing, recall. Output 1 is locked by someone else → skipped.
        self.assertEqual(self.post("/api/salvos/save", {"name": "A", "outputs": {"video": "all"}})[0], 200)
        self.assertEqual([s["name"] for s in self.state()["salvos"]], ["A"])
        self.post("/api/route", {"routes": [[0, 0], [2, 3]]})
        self.assertTrue(wait_for(lambda: self.state()["connection"]["hub"]["routing"] == [0, 3, 3]))
        status, res = self.post("/api/salvos/recall", {"name": "A"})
        self.assertEqual((status, res["skipped"]), (200, 1))
        self.assertTrue(wait_for(lambda: self.state()["connection"]["hub"]["routing"] == [2, 3, 0]))

        self.assertEqual(self.post("/api/route", {"routes": [["x", 1]]})[0], 400)
        self.assertEqual(self.post("/api/hubs/save", {"name": "Régie", "host": "127.0.0.1", "port": self.hub.port})[0], 200)
        self.assertEqual(self.state()["hubs"][0]["name"], "Régie")

    def test_static_and_traversal(self):
        with urllib.request.urlopen(self.base + "/") as r:
            self.assertIn(b"Videohub Control", r.read())
        with self.assertRaises(urllib.error.HTTPError):
            urllib.request.urlopen(self.base + "/../server.py")


if __name__ == "__main__":
    unittest.main()
