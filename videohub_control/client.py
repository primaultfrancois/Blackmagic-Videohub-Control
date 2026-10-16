"""Threaded TCP client for a Videohub, with auto-reconnect and keepalive."""

from __future__ import annotations

import queue
import select
import socket
import threading
import time
from typing import Callable

from .protocol import (
    DEFAULT_PORT,
    LABEL_BLOCKS,
    LOCK_BLOCKS,
    ROUTING_BLOCKS,
    BlockParser,
    HubState,
    encode_block,
    sanitize_label,
)

PING_INTERVAL = 10.0
ACK_TIMEOUT = 3.0


class CommandError(Exception):
    pass


class VideohubClient:
    """One connection to one Videohub. `on_change` is called (from the reader
    thread) whenever hub state or connection status changes."""

    def __init__(self, host: str, port: int = DEFAULT_PORT,
                 on_change: Callable[[], None] | None = None) -> None:
        self.host = host
        self.port = port
        self.on_change = on_change or (lambda: None)
        self.state = HubState()
        self.status = "disconnected"   # disconnected | connecting | connected
        self.error = ""
        self._lock = threading.RLock()       # guards state / status
        self._send_lock = threading.Lock()   # serializes command + ACK wait
        self._acks: queue.Queue[str] = queue.Queue()
        self._sock: socket.socket | None = None
        self._ping_outstanding = False
        self._stop = threading.Event()
        self._thread: threading.Thread | None = None

    # --- lifecycle --------------------------------------------------------
    def start(self) -> None:
        self._stop.clear()
        self._thread = threading.Thread(target=self._run, name=f"videohub-{self.host}", daemon=True)
        self._thread.start()

    def stop(self) -> None:
        self._stop.set()
        sock = self._sock
        if sock:
            try:
                sock.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
        if self._thread:
            self._thread.join(timeout=3)
        self._set_status("disconnected")

    def snapshot(self) -> dict:
        with self._lock:
            return {
                "host": self.host,
                "port": self.port,
                "status": self.status,
                "error": self.error,
                "hub": self.state.to_dict() if self.status == "connected" else None,
            }

    def _set_status(self, status: str, error: str = "") -> None:
        with self._lock:
            if (self.status, self.error) == (status, error):
                return
            self.status, self.error = status, error
        self.on_change()

    def _run(self) -> None:
        backoff = 1.0
        while not self._stop.is_set():
            self._set_status("connecting", self.error)
            try:
                sock = socket.create_connection((self.host, self.port), timeout=5)
            except OSError as exc:
                self._set_status("connecting", f"Connexion impossible : {exc.strerror or exc}")
                self._stop.wait(backoff)
                backoff = min(backoff * 2, 10.0)
                continue
            backoff = 1.0
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_KEEPALIVE, 1)
            self._sock = sock
            with self._lock:
                self.state = HubState()
            self._ping_outstanding = False
            try:
                self._read_loop(sock)
            except OSError as exc:
                if not self._stop.is_set():
                    self._set_status("connecting", f"Connexion perdue : {exc.strerror or exc}")
            finally:
                self._sock = None
                sock.close()
            if not self._stop.is_set():
                self._stop.wait(1.0)

    def _read_loop(self, sock: socket.socket) -> None:
        parser = BlockParser()
        last_rx = time.monotonic()
        ping_sent = 0.0
        while not self._stop.is_set():
            readable, _, _ = select.select([sock], [], [], 1.0)
            now = time.monotonic()
            if readable:
                data = sock.recv(65536)
                if not data:
                    raise OSError("fermée par le Videohub")
                last_rx, ping_sent = now, 0.0
                self._handle(parser.feed(data))
            elif ping_sent and now - ping_sent > PING_INTERVAL:
                raise OSError("pas de réponse au ping")
            elif not ping_sent and now - last_rx > PING_INTERVAL and self.status == "connected":
                # Skip if a command is in flight; its ACK proves liveness.
                if self._send_lock.acquire(blocking=False):
                    try:
                        self._ping_outstanding = True
                        sock.sendall(encode_block("PING"))
                    finally:
                        self._send_lock.release()
                    ping_sent = now

    def _handle(self, blocks) -> None:
        changed = False
        for block in blocks:
            if block.header in ("ACK", "NAK"):
                # Replies arrive in order, so the first one after a ping is its.
                if self._ping_outstanding:
                    self._ping_outstanding = False
                else:
                    self._acks.put(block.header)
                continue
            with self._lock:
                changed |= self.state.apply(block)
            if block.header == "END PRELUDE":
                self._set_status("connected")
                changed = True
        if changed and self.status == "connected":
            self.on_change()

    # --- commands ---------------------------------------------------------
    def send(self, header: str, lines: list[str]) -> None:
        if self.status != "connected" or self._sock is None:
            raise CommandError("Videohub non connecté")
        with self._send_lock:
            while not self._acks.empty():
                self._acks.get_nowait()
            try:
                self._sock.sendall(encode_block(header, lines))
            except OSError as exc:
                raise CommandError(f"Envoi impossible : {exc}") from exc
            try:
                reply = self._acks.get(timeout=ACK_TIMEOUT)
            except queue.Empty:
                raise CommandError("Pas de réponse du Videohub") from None
            if reply != "ACK":
                raise CommandError("Commande refusée par le Videohub (NAK)")

    def route(self, kind: str, routes: list[tuple[int, int]]) -> None:
        if kind not in ROUTING_BLOCKS:
            raise CommandError(f"Type de routage inconnu : {kind}")
        if routes:
            self.send(ROUTING_BLOCKS[kind], [f"{int(o)} {int(i)}" for o, i in routes])

    def set_labels(self, kind: str, labels: list[tuple[int, str]]) -> None:
        if kind not in LABEL_BLOCKS:
            raise CommandError(f"Type d'étiquette inconnu : {kind}")
        if labels:
            self.send(LABEL_BLOCKS[kind], [f"{int(i)} {sanitize_label(t)}" for i, t in labels])

    def set_locks(self, kind: str, locks: list[tuple[int, str]]) -> None:
        if kind not in LOCK_BLOCKS:
            raise CommandError(f"Type de verrou inconnu : {kind}")
        for _, value in locks:
            if value not in ("O", "U", "F"):
                raise CommandError(f"État de verrou invalide : {value}")
        if locks:
            self.send(LOCK_BLOCKS[kind], [f"{int(i)} {v}" for i, v in locks])
