"""Entry point: python3 -m videohub_control [--bind ADDR] [--port N] [--open]"""

from __future__ import annotations

import argparse
import threading
import webbrowser
from pathlib import Path

from . import __version__
from .server import make_server
from .store import Store


def main() -> None:
    parser = argparse.ArgumentParser(prog="videohub-control",
                                     description="Contrôle de routeurs Blackmagic Videohub (interface web).")
    parser.add_argument("--bind", default="127.0.0.1",
                        help="adresse d'écoute (0.0.0.0 pour y accéder depuis le réseau ; défaut 127.0.0.1)")
    parser.add_argument("--port", type=int, default=8090, help="port HTTP (défaut 8090)")
    parser.add_argument("--config", type=Path, help="fichier de configuration JSON")
    parser.add_argument("--hub", help="adresse IP d'un Videohub à connecter au démarrage")
    parser.add_argument("--open", action="store_true", help="ouvrir le navigateur au démarrage")
    args = parser.parse_args()

    store = Store(args.config)
    httpd, controller = make_server(args.bind, args.port, store)

    if args.hub:
        host, _, port = args.hub.partition(":")
        controller.connect(host, int(port or 9990))
    elif store.data.get("lastHub"):
        last = store.data["lastHub"]
        controller.connect(last["host"], int(last.get("port") or 9990))

    shown = "localhost" if args.bind in ("127.0.0.1", "0.0.0.0", "::") else args.bind
    url = f"http://{shown}:{args.port}/"
    print(f"Videohub Control {__version__} — {url}  (Ctrl+C pour quitter)")
    if args.open:
        threading.Timer(0.5, webbrowser.open, (url,)).start()
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        if controller.client:
            controller.client.stop()
        httpd.server_close()


if __name__ == "__main__":
    main()
