// Command videohub-control is the desktop version of Videohub Control: a
// single native window, no web server, no open port.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	webui "github.com/primaultfrancois/Blackmagic-Videohub-Control"
	"github.com/primaultfrancois/Blackmagic-Videohub-Control/internal/app"
	"github.com/primaultfrancois/Blackmagic-Videohub-Control/internal/gui"
	"github.com/primaultfrancois/Blackmagic-Videohub-Control/internal/store"
)

const version = "7.0.6"

// bridgeJS is injected before app.js. It exposes window.__vhNative, which
// app.js uses instead of fetch()/EventSource when present.
const bridgeJS = `(() => {
  const pending = new Map();
  let seq = 0;
  window.__vhNative = {
    onState: null,
    call(path, body) {
      return new Promise((resolve, reject) => {
        const id = ++seq;
        pending.set(id, { resolve, reject });
        window.webkit.messageHandlers.vh.postMessage(JSON.stringify({ id, path, body: body || {} }));
      });
    },
  };
  window.__vhReply = (id, ok, data) => {
    const p = pending.get(id);
    if (!p) return;
    pending.delete(id);
    ok ? p.resolve(data) : p.reject(new Error(data));
  };
  window.__vhState = (s) => { if (window.__vhNative.onState) window.__vhNative.onState(s); };
})();`

type message struct {
	ID   int64           `json:"id"`
	Path string          `json:"path"`
	Body json.RawMessage `json:"body"`
}

func reply(id int64, result any, err error) {
	if err != nil {
		msg, _ := json.Marshal(err.Error())
		gui.Eval(fmt.Sprintf("window.__vhReply(%d,false,%s)", id, msg))
		return
	}
	data, merr := json.Marshal(result)
	if merr != nil {
		data = []byte("null")
	}
	gui.Eval(fmt.Sprintf("window.__vhReply(%d,true,%s)", id, data))
}

// saveFile shows a native save dialog (the webview can't download blobs).
func saveFile(id int64, body json.RawMessage) {
	var req struct{ Name, Content string }
	json.Unmarshal(body, &req)
	gui.Dispatch(func() {
		path := gui.SaveDialog(req.Name)
		if path == "" {
			reply(id, map[string]any{"ok": false}, nil)
			return
		}
		if err := os.WriteFile(path, []byte(req.Content), 0o644); err != nil {
			reply(id, nil, fmt.Errorf("Écriture impossible : %v", err))
			return
		}
		reply(id, map[string]any{"ok": true, "path": path}, nil)
	})
}

func main() {
	hub := flag.String("hub", "", "adresse IP d'un Videohub à connecter au démarrage (ip ou ip:port)")
	config := flag.String("config", "", "fichier de configuration JSON (défaut ~/.config/videohub-control/config.json)")
	devtools := flag.Bool("devtools", false, "active l'inspecteur web (clic droit → Inspecter)")
	showVersion := flag.Bool("version", false, "affiche la version")
	flag.Parse()
	if *showVersion {
		fmt.Println("Videohub Control", version)
		return
	}

	ctl := app.New(store.Open(*config))

	// Coalesce state pushes: at most one snapshot per ~30 ms burst.
	var pushPending atomic.Bool
	ctl.OnChange = func() {
		if !pushPending.CompareAndSwap(false, true) {
			return
		}
		time.AfterFunc(30*time.Millisecond, func() {
			pushPending.Store(false)
			data, err := json.Marshal(ctl.Snapshot())
			if err == nil {
				gui.Eval("window.__vhState(" + string(data) + ")")
			}
		})
	}

	if *hub != "" {
		host, portStr, _ := strings.Cut(*hub, ":")
		port, _ := strconv.Atoi(portStr)
		ctl.Connect(host, port)
	} else if last := ctl.Store.LastHub(); last != nil {
		ctl.Connect(last.Host, last.Port)
	}

	gui.Run(gui.Options{
		Title:    "Videohub Control",
		Width:    1440,
		Height:   900,
		HTML:     webui.Page(),
		BridgeJS: bridgeJS,
		DevTools: *devtools,
		OnMessage: func(raw string) {
			var m message
			if json.Unmarshal([]byte(raw), &m) != nil {
				return
			}
			if m.Path == "saveFile" {
				saveFile(m.ID, m.Body)
				return
			}
			go func() {
				result, err := ctl.Handle(m.Path, m.Body)
				reply(m.ID, result, err)
			}()
		},
	})
	ctl.Close()
}
