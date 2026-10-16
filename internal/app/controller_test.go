package app

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/primaultfrancois/Blackmagic-Videohub-Control/internal/store"
	"github.com/primaultfrancois/Blackmagic-Videohub-Control/internal/videohub"
)

const prelude = "PROTOCOL PREAMBLE:\nVersion: 2.8\n\n" +
	"VIDEOHUB DEVICE:\nDevice present: true\nModel name: Smart Videohub 12G 40x40\n" +
	"Friendly name: Régie\nUnique ID: 7C2E0D021714\nVideo inputs: 4\n" +
	"Video outputs: 3\nVideo monitoring outputs: 1\n\n" +
	"OUTPUT LABELS:\n0 PGM\n1 Multiview\n2 Rec\n\n" +
	"VIDEO OUTPUT LOCKS:\n0 U\n1 L\n2 O\n\n" +
	"VIDEO OUTPUT ROUTING:\n0 2\n1 1\n2 0\n\n" +
	"VIDEO MONITORING OUTPUT ROUTING:\n0 3\n\n" +
	"END PRELUDE:\n\n"

// fakeHub sends the prelude, ACKs commands (or NAKs input labels) and echoes
// every command back as a status update, like a real Videohub.
type fakeHub struct {
	ln       net.Listener
	mu       sync.Mutex
	received []string
}

func newFakeHub(t *testing.T, nakLabels bool) *fakeHub {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := &fakeHub{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.Write([]byte(prelude))
		r := bufio.NewReader(conn)
		var block []string
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\n")
			if line != "" {
				block = append(block, line)
				continue
			}
			if len(block) == 0 {
				continue
			}
			text := strings.Join(block, "\n")
			block = nil
			h.mu.Lock()
			h.received = append(h.received, text)
			h.mu.Unlock()
			if nakLabels && strings.HasPrefix(text, "INPUT LABELS:") {
				conn.Write([]byte("NAK\n\n"))
				continue
			}
			conn.Write([]byte("ACK\n\n"))
			if !strings.HasPrefix(text, "PING:") {
				conn.Write([]byte(text + "\n\n"))
			}
		}
	}()
	return h
}

func (h *fakeHub) port() int { return h.ln.Addr().(*net.TCPAddr).Port }

func (h *fakeHub) got(text string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.received {
		if r == text {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func setup(t *testing.T, nakLabels bool) (*Controller, *fakeHub) {
	hub := newFakeHub(t, nakLabels)
	ctl := New(store.Open(filepath.Join(t.TempDir(), "config.json")))
	t.Cleanup(ctl.Close)
	return ctl, hub
}

func call(t *testing.T, ctl *Controller, path, body string) (map[string]any, error) {
	t.Helper()
	res, err := ctl.Handle(path, []byte(body))
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(res)
	var out map[string]any
	json.Unmarshal(raw, &out)
	return out, nil
}

func hubState(ctl *Controller) *videohub.State {
	s := ctl.Snapshot()
	if s.Connection == nil {
		return nil
	}
	return s.Connection.Hub
}

func TestFullFlow(t *testing.T) {
	ctl, hub := setup(t, false)

	if _, err := call(t, ctl, "route", `{"routes":[[0,1]]}`); err == nil || !IsUserFacing(err) {
		t.Fatalf("route without hub: %v", err)
	}
	if _, err := call(t, ctl, "connect", `{"host":"127.0.0.1","port":`+itoa(hub.port())+`}`); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "connected", func() bool { return hubState(ctl) != nil })

	if _, err := call(t, ctl, "route", `{"kind":"video","routes":[[1,3]]}`); err != nil {
		t.Fatal(err)
	}
	if !hub.got("VIDEO OUTPUT ROUTING:\n1 3") {
		t.Fatal("hub did not receive routing command")
	}
	waitFor(t, "routing", func() bool { return reflect.DeepEqual(hubState(ctl).Routing, []int{2, 3, 0}) })

	if _, err := call(t, ctl, "labels", `{"kind":"output","labels":[[0,"Programme\nA"]]}`); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "label", func() bool { return hubState(ctl).OutputLabels[0] == "Programme A" })

	if _, err := call(t, ctl, "locks", `{"locks":[[0,"O"]]}`); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "lock", func() bool { return hubState(ctl).Locks[0] == "O" })

	// Salvo: save, change routing, recall. Output 1 is locked by someone else → skipped.
	if _, err := call(t, ctl, "salvos/save", `{"name":"  A  ","outputs":{"video":"all","monitor":[0,5]}}`); err != nil {
		t.Fatal(err)
	}
	salvos := ctl.Snapshot().Salvos
	if len(salvos) != 1 || salvos[0].Name != "A" || len(salvos[0].Monitor) != 1 {
		t.Fatalf("salvos: %+v", salvos)
	}
	call(t, ctl, "route", `{"routes":[[0,0],[2,3]]}`)
	waitFor(t, "changed", func() bool { return reflect.DeepEqual(hubState(ctl).Routing, []int{0, 3, 3}) })
	res, err := call(t, ctl, "salvos/recall", `{"name":"A"}`)
	if err != nil || res["skipped"] != float64(1) {
		t.Fatalf("recall: %v %v", res, err)
	}
	waitFor(t, "recalled", func() bool { return reflect.DeepEqual(hubState(ctl).Routing, []int{2, 3, 0}) })

	if _, err := call(t, ctl, "route", `{"routes":[["x",1]]}`); err == nil {
		t.Fatal("expected error for invalid route")
	}
	if _, err := call(t, ctl, "hubs/save", `{"name":"Régie","host":"127.0.0.1","port":9990}`); err != nil {
		t.Fatal(err)
	}
	if hubs := ctl.Snapshot().Hubs; len(hubs) != 1 || hubs[0].Name != "Régie" {
		t.Fatalf("hubs: %+v", hubs)
	}
	if _, err := call(t, ctl, "nope", `{}`); err == nil {
		t.Fatal("expected unknown action error")
	}
}

func TestNakAndInvalidLock(t *testing.T) {
	ctl, hub := setup(t, true)
	call(t, ctl, "connect", `{"host":"127.0.0.1","port":`+itoa(hub.port())+`}`)
	waitFor(t, "connected", func() bool { return hubState(ctl) != nil })

	_, err := call(t, ctl, "labels", `{"kind":"input","labels":[[0,"X"]]}`)
	var ce *videohub.CommandError
	if !errors.As(err, &ce) || !strings.Contains(ce.Msg, "NAK") {
		t.Fatalf("expected NAK error, got %v", err)
	}
	if _, err := call(t, ctl, "locks", `{"locks":[[0,"Z"]]}`); err == nil {
		t.Fatal("expected invalid lock error")
	}
}

func TestSnapshotJSONShape(t *testing.T) {
	ctl, _ := setup(t, false)
	raw, _ := json.Marshal(ctl.Snapshot())
	var m map[string]any
	json.Unmarshal(raw, &m)
	for _, k := range []string{"hubs", "salvos", "discovered"} {
		if _, ok := m[k].([]any); !ok {
			t.Errorf("%s should be a JSON array, got %v", k, m[k])
		}
	}
	if m["connection"] != nil {
		t.Errorf("connection should be null when disconnected")
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
