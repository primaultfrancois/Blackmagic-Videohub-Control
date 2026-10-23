# Videohub Control for Linux

A Linux recreation of **Blackmagic Videohub Control**. It controls Blackmagic Videohub
routers over the Videohub Ethernet protocol (TCP 9990).

![Routing panel](docs/panneau.png)

![Crosspoint matrix](docs/matrice.png)

## Features

- **Routing panel**: pick a destination, click a source to route. Ctrl/Shift+click to route several destinations at once.
- **Take mode**: arm a source, then confirm with TAKE or Enter.
- **Crosspoint matrix**: up to 288×288.
- **Labels**: edit input/output names, CSV import/export.
- **Salvos**: save and recall routing presets, stored per Videohub.
- **Locks**, monitoring outputs, network discovery (mDNS), favorites, auto-reconnect.

## Two versions, same interface

| | Desktop (Go) | Web (Python) |
|---|---|---|
| Window | native GTK + WebKit | any browser, tablets included |
| Network | no server, no open port | local HTTP server (port 8090) |

Both share the same UI and the same config file (`~/.config/videohub-control/config.json`).

## Install

Download from the [Releases](https://github.com/primaultfrancois/Blackmagic-Videohub-Control/releases) page:

- **Desktop**: unpack `videohub-control-…-linux-amd64.tar.gz` and run `./videohub-control`
  (needs `libwebkit2gtk-4.1-0`, already present on Ubuntu desktop).
- **Web**: `pip install videohub_control-…-py3-none-any.whl`

## Usage

```sh
./videohub-control                      # reconnect to the last hub
./videohub-control --hub 10.0.0.50      # connect directly

python3 -m videohub_control --open      # web version on http://localhost:8090
```

> `--bind 0.0.0.0` exposes the web version to the network **without authentication**.

## Build

```sh
sudo apt install golang-go libwebkit2gtk-4.1-dev
go build -o videohub-control ./cmd/videohub-control
```

Tests: `go test ./...` and `python3 -m unittest discover -s tests -t .`
