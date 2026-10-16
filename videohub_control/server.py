"""Local HTTP server: serves the web UI, a JSON API and a Server-Sent Events
stream that pushes the full state whenever it changes."""

from __future__ import annotations

import json
import mimetypes
import threading
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlparse

from .client import CommandError, VideohubClient
from .discovery import discover
from .protocol import DEFAULT_PORT, LOCK_LOCKED
from .store import Store

WEB_DIR = Path(__file__).parent / "web"


class ApiError(Exception):
    def __init__(self, message: str, status: int = HTTPStatus.BAD_REQUEST) -> None:
        super().__init__(message)
        self.status = status


class Controller:
    def __init__(self, store: Store) -> None:
        self.store = store
        self.client: VideohubClient | None = None
        self.discovered: list[dict] = []
        self.version = 0
        self._cond = threading.Condition()
        self._lock = threading.Lock()

    # --- change notification ---------------------------------------------
    def bump(self) -> None:
        with self._cond:
            self.version += 1
            self._cond.notify_all()

    def wait_change(self, seen: int, timeout: float) -> int:
        with self._cond:
            self._cond.wait_for(lambda: self.version != seen, timeout=timeout)
            return self.version

    # --- state ------------------------------------------------------------
    def hub_key(self) -> str | None:
        c = self.client
        if not c or c.status != "connected":
            return None
        return c.state.unique_id or f"{c.host}:{c.port}"

    def snapshot(self) -> dict:
        key = self.hub_key()
        return {
            "version": self.version,
            "connection": self.client.snapshot() if self.client else None,
            "hubs": self.store.hubs(),
            "salvos": self.store.salvos(key) if key else [],
            "discovered": self.discovered,
        }

    def require_client(self) -> VideohubClient:
        c = self.client
        if not c or c.status != "connected":
            raise ApiError("Aucun Videohub connecté", HTTPStatus.CONFLICT)
        return c

    # --- actions ----------------------------------------------------------
    def connect(self, host: str, port: int) -> None:
        host = host.strip()
        if not host:
            raise ApiError("Adresse IP manquante")
        with self._lock:
            if self.client:
                self.client.on_change = lambda: None
                self.client.stop()
            self.client = VideohubClient(host, port, on_change=self.bump)
            self.client.start()
        self.store.set_last_hub(host, port)
        self.bump()

    def disconnect(self) -> None:
        with self._lock:
            if self.client:
                self.client.on_change = lambda: None
                self.client.stop()
                self.client = None
        self.store.set_last_hub(None, None)
        self.bump()

    def run_discovery(self) -> list[dict]:
        self.discovered = discover()
        self.bump()
        return self.discovered

    def save_salvo(self, name: str, outputs: dict) -> None:
        c = self.require_client()
        name = " ".join(str(name).split())
        if not name:
            raise ApiError("Nom de salve manquant")
        with c._lock:
            routing, mon_routing = list(c.state.routing), list(c.state.monitor_routing)
        video = [[o, routing[o]] for o in _indexes(outputs.get("video"), len(routing))]
        monitor = [[o, mon_routing[o]] for o in _indexes(outputs.get("monitor"), len(mon_routing))]
        self.store.save_salvo(self.hub_key(), {"name": name, "video": video, "monitor": monitor})
        self.bump()

    def recall_salvo(self, name: str) -> dict:
        c = self.require_client()
        salvo = self.store.get_salvo(self.hub_key(), name)
        if not salvo:
            raise ApiError("Salve introuvable", HTTPStatus.NOT_FOUND)
        skipped = 0
        for kind, locks, size in (("video", c.state.locks, c.state.video_outputs),
                                  ("monitor", c.state.monitor_locks, c.state.monitoring_outputs)):
            routes = []
            for o, i in salvo.get(kind, []):
                if not 0 <= o < size:
                    continue
                if locks[o] == LOCK_LOCKED:
                    skipped += 1
                    continue
                routes.append((o, i))
            c.route(kind, routes)
        return {"skipped": skipped}


def _indexes(value, size: int) -> list[int]:
    if value == "all":
        return list(range(size))
    if not isinstance(value, list):
        return []
    return sorted({int(v) for v in value if isinstance(v, int) and 0 <= v < size})


def _pairs(value, second_type) -> list[tuple]:
    if not isinstance(value, list):
        raise ApiError("Liste attendue")
    out = []
    for item in value:
        if not (isinstance(item, list) and len(item) == 2 and isinstance(item[0], int)
                and isinstance(item[1], second_type)):
            raise ApiError(f"Entrée invalide : {item!r}")
        out.append((item[0], item[1]))
    return out


class Handler(BaseHTTPRequestHandler):
    controller: Controller
    server_version = "VideohubControl"

    def log_message(self, fmt, *args) -> None:  # keep the console quiet
        pass

    # --- helpers ----------------------------------------------------------
    def _json(self, payload, status: int = HTTPStatus.OK) -> None:
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(body)

    def _body(self) -> dict:
        length = int(self.headers.get("Content-Length") or 0)
        if length > 1_000_000:
            raise ApiError("Requête trop grande")
        try:
            data = json.loads(self.rfile.read(length) or b"{}")
        except ValueError:
            raise ApiError("JSON invalide") from None
        if not isinstance(data, dict):
            raise ApiError("Objet JSON attendu")
        return data

    # --- GET --------------------------------------------------------------
    def do_GET(self) -> None:
        path = urlparse(self.path).path
        if path == "/api/state":
            return self._json(self.controller.snapshot())
        if path == "/api/events":
            return self._events()
        if path == "/":
            path = "/index.html"
        file = (WEB_DIR / path.lstrip("/")).resolve()
        if WEB_DIR.resolve() not in file.parents or not file.is_file():
            return self._json({"error": "Introuvable"}, HTTPStatus.NOT_FOUND)
        body = file.read_bytes()
        ctype = mimetypes.guess_type(file.name)[0] or "application/octet-stream"
        if ctype.startswith("text/") or ctype.endswith("javascript"):
            ctype += "; charset=utf-8"
        self.send_response(HTTPStatus.OK)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-cache")
        self.end_headers()
        self.wfile.write(body)

    def _events(self) -> None:
        self.send_response(HTTPStatus.OK)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        seen = -1
        try:
            while True:
                version = self.controller.wait_change(seen, timeout=15)
                if version == seen:
                    self.wfile.write(b": keepalive\n\n")
                else:
                    seen = version
                    data = json.dumps(self.controller.snapshot(), ensure_ascii=False)
                    self.wfile.write(f"data: {data}\n\n".encode("utf-8"))
                self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError, OSError):
            pass

    # --- POST -------------------------------------------------------------
    def do_POST(self) -> None:
        ctl = self.controller
        path = urlparse(self.path).path
        try:
            body = self._body()
            result: dict = {}
            if path == "/api/connect":
                ctl.connect(str(body.get("host", "")), int(body.get("port") or DEFAULT_PORT))
            elif path == "/api/disconnect":
                ctl.disconnect()
            elif path == "/api/discover":
                result = {"discovered": ctl.run_discovery()}
            elif path == "/api/route":
                ctl.require_client().route(body.get("kind", "video"), _pairs(body.get("routes"), int))
            elif path == "/api/labels":
                ctl.require_client().set_labels(body.get("kind", ""), _pairs(body.get("labels"), str))
            elif path == "/api/locks":
                ctl.require_client().set_locks(body.get("kind", "video"), _pairs(body.get("locks"), str))
            elif path == "/api/hubs/save":
                ctl.store.save_hub(str(body.get("name", "")).strip(), str(body.get("host", "")).strip(),
                                   int(body.get("port") or DEFAULT_PORT))
                ctl.bump()
            elif path == "/api/hubs/delete":
                ctl.store.delete_hub(str(body.get("host", "")), int(body.get("port") or DEFAULT_PORT))
                ctl.bump()
            elif path == "/api/salvos/save":
                ctl.save_salvo(body.get("name", ""), body.get("outputs") or {})
            elif path == "/api/salvos/delete":
                ctl.require_client()
                ctl.store.delete_salvo(ctl.hub_key(), str(body.get("name", "")))
                ctl.bump()
            elif path == "/api/salvos/recall":
                result = ctl.recall_salvo(str(body.get("name", "")))
            else:
                raise ApiError("Introuvable", HTTPStatus.NOT_FOUND)
            self._json({"ok": True, **result})
        except ApiError as exc:
            self._json({"error": str(exc)}, exc.status)
        except CommandError as exc:
            self._json({"error": str(exc)}, HTTPStatus.BAD_GATEWAY)
        except (TypeError, ValueError) as exc:
            self._json({"error": f"Paramètre invalide : {exc}"}, HTTPStatus.BAD_REQUEST)


def make_server(bind: str, port: int, store: Store) -> tuple[ThreadingHTTPServer, Controller]:
    controller = Controller(store)
    handler = type("BoundHandler", (Handler,), {"controller": controller})
    httpd = ThreadingHTTPServer((bind, port), handler)
    httpd.daemon_threads = True
    return httpd, controller
