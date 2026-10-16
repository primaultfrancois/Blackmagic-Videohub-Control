"""Persistent settings: saved Videohubs and salvos (JSON in ~/.config)."""

from __future__ import annotations

import json
import os
import threading
from pathlib import Path


def default_path() -> Path:
    base = os.environ.get("XDG_CONFIG_HOME") or Path.home() / ".config"
    return Path(base) / "videohub-control" / "config.json"


class Store:
    def __init__(self, path: Path | None = None) -> None:
        self.path = path or default_path()
        self._lock = threading.Lock()
        self.data = {"hubs": [], "salvos": {}, "lastHub": None}
        try:
            loaded = json.loads(self.path.read_text("utf-8"))
            if isinstance(loaded, dict):
                self.data.update(loaded)
        except (OSError, ValueError):
            pass

    def _save(self) -> None:
        self.path.parent.mkdir(parents=True, exist_ok=True)
        tmp = self.path.with_suffix(".tmp")
        tmp.write_text(json.dumps(self.data, indent=2, ensure_ascii=False), "utf-8")
        tmp.replace(self.path)

    # --- hubs -------------------------------------------------------------
    def hubs(self) -> list[dict]:
        return list(self.data["hubs"])

    def save_hub(self, name: str, host: str, port: int) -> None:
        with self._lock:
            hubs = [h for h in self.data["hubs"] if (h["host"], h["port"]) != (host, port)]
            hubs.append({"name": name or host, "host": host, "port": port})
            hubs.sort(key=lambda h: h["name"].lower())
            self.data["hubs"] = hubs
            self._save()

    def delete_hub(self, host: str, port: int) -> None:
        with self._lock:
            self.data["hubs"] = [h for h in self.data["hubs"] if (h["host"], h["port"]) != (host, port)]
            self._save()

    def set_last_hub(self, host: str | None, port: int | None) -> None:
        with self._lock:
            self.data["lastHub"] = {"host": host, "port": port} if host else None
            self._save()

    # --- salvos (keyed by hub unique ID so they follow the device) ---------
    def salvos(self, hub_key: str) -> list[dict]:
        return list(self.data["salvos"].get(hub_key, []))

    def save_salvo(self, hub_key: str, salvo: dict) -> None:
        with self._lock:
            salvos = [s for s in self.data["salvos"].get(hub_key, []) if s["name"] != salvo["name"]]
            salvos.append(salvo)
            salvos.sort(key=lambda s: s["name"].lower())
            self.data["salvos"][hub_key] = salvos
            self._save()

    def delete_salvo(self, hub_key: str, name: str) -> None:
        with self._lock:
            self.data["salvos"][hub_key] = [s for s in self.data["salvos"].get(hub_key, []) if s["name"] != name]
            self._save()

    def get_salvo(self, hub_key: str, name: str) -> dict | None:
        return next((s for s in self.data["salvos"].get(hub_key, []) if s["name"] == name), None)
