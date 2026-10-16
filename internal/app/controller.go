// Package app holds the application logic shared by the UI bridge: the
// current Videohub connection, saved hubs, salvos and discovery. Actions use
// the same names and payloads as the HTTP API of the Python version, so the
// web UI talks to both identically.
package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/primaultfrancois/Blackmagic-Videohub-Control/internal/discovery"
	"github.com/primaultfrancois/Blackmagic-Videohub-Control/internal/store"
	"github.com/primaultfrancois/Blackmagic-Videohub-Control/internal/videohub"
)

// UserError is an error whose message is meant for the UI.
type UserError struct{ Msg string }

func (e *UserError) Error() string { return e.Msg }

func userErr(format string, a ...any) error { return &UserError{fmt.Sprintf(format, a...)} }

type Snapshot struct {
	Version    int64                  `json:"version"`
	Connection *videohub.ConnSnapshot `json:"connection"`
	Hubs       []store.Hub            `json:"hubs"`
	Salvos     []store.Salvo          `json:"salvos"`
	Discovered []discovery.Device     `json:"discovered"`
	Theme      string                 `json:"theme"`
}

type Controller struct {
	Store    *store.Store
	OnChange func() // called whenever the snapshot may have changed

	mu         sync.Mutex
	client     *videohub.Client
	discovered []discovery.Device
	version    int64
}

func New(st *store.Store) *Controller {
	return &Controller{Store: st, OnChange: func() {}, discovered: []discovery.Device{}}
}

func (c *Controller) bump() {
	c.mu.Lock()
	c.version++
	c.mu.Unlock()
	c.OnChange()
}

func (c *Controller) current() *videohub.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client
}

func hubKey(cl *videohub.Client, st *videohub.State) string {
	if st.UniqueID != "" {
		return st.UniqueID
	}
	return fmt.Sprintf("%s:%d", cl.Host, cl.Port)
}

func (c *Controller) Snapshot() Snapshot {
	c.mu.Lock()
	cl, disc, ver := c.client, c.discovered, c.version
	c.mu.Unlock()
	s := Snapshot{Version: ver, Hubs: c.Store.Hubs(), Salvos: []store.Salvo{}, Discovered: disc, Theme: c.Store.Theme()}
	if cl != nil {
		conn := cl.Snapshot()
		s.Connection = &conn
		if conn.Hub != nil {
			s.Salvos = c.Store.Salvos(hubKey(cl, conn.Hub))
		}
	}
	return s
}

func (c *Controller) requireClient() (*videohub.Client, error) {
	cl := c.current()
	if cl == nil || cl.Status() != "connected" {
		return nil, userErr("Aucun Videohub connecté")
	}
	return cl, nil
}

func (c *Controller) Connect(host string, port int) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return userErr("Adresse IP manquante")
	}
	if port == 0 {
		port = videohub.DefaultPort
	}
	c.mu.Lock()
	old := c.client
	c.client = videohub.NewClient(host, port, c.bump)
	cl := c.client
	c.mu.Unlock()
	if old != nil {
		old.Stop()
	}
	cl.Start()
	c.Store.SetLastHub(host, port)
	c.bump()
	return nil
}

func (c *Controller) Disconnect() {
	c.mu.Lock()
	old := c.client
	c.client = nil
	c.mu.Unlock()
	if old != nil {
		old.Stop()
	}
	c.Store.SetLastHub("", 0)
	c.bump()
}

// Close stops the connection without forgetting it as the last hub.
func (c *Controller) Close() {
	if cl := c.current(); cl != nil {
		cl.Stop()
	}
}

// --- request handling ----------------------------------------------------------

type pairList [][2]json.RawMessage

func (p pairList) ints() ([]videohub.IntPair, error) {
	out := make([]videohub.IntPair, len(p))
	for i, item := range p {
		if json.Unmarshal(item[0], &out[i][0]) != nil || json.Unmarshal(item[1], &out[i][1]) != nil {
			return nil, userErr("Entrée invalide : [%s, %s]", item[0], item[1])
		}
	}
	return out, nil
}

func (p pairList) strs() ([]videohub.LabelPair, error) {
	out := make([]videohub.LabelPair, len(p))
	for i, item := range p {
		if json.Unmarshal(item[0], &out[i].Index) != nil || json.Unmarshal(item[1], &out[i].Text) != nil {
			return nil, userErr("Entrée invalide : [%s, %s]", item[0], item[1])
		}
	}
	return out, nil
}

type request struct {
	Host    string                     `json:"host"`
	Port    int                        `json:"port"`
	Name    string                     `json:"name"`
	Kind    string                     `json:"kind"`
	Routes  pairList                   `json:"routes"`
	Labels  pairList                   `json:"labels"`
	Locks   pairList                   `json:"locks"`
	Outputs map[string]json.RawMessage `json:"outputs"`
	Theme   string                     `json:"theme"`
}

// Handle runs one UI action. Errors are *UserError or *videohub.CommandError,
// both carrying a message for the user.
func (c *Controller) Handle(path string, body []byte) (any, error) {
	var req request
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, userErr("Paramètre invalide : %v", err)
		}
	}
	if req.Kind == "" && (path == "route" || path == "locks") {
		req.Kind = "video"
	}
	if req.Port == 0 {
		req.Port = videohub.DefaultPort
	}
	ok := map[string]any{"ok": true}

	switch path {
	case "state":
		return c.Snapshot(), nil
	case "connect":
		return ok, c.Connect(req.Host, req.Port)
	case "disconnect":
		c.Disconnect()
		return ok, nil
	case "discover":
		devs := discovery.Discover(2 * time.Second)
		c.mu.Lock()
		c.discovered = devs
		c.mu.Unlock()
		c.bump()
		return map[string]any{"ok": true, "discovered": devs}, nil
	case "route":
		cl, err := c.requireClient()
		if err != nil {
			return nil, err
		}
		routes, err := req.Routes.ints()
		if err != nil {
			return nil, err
		}
		return ok, cl.Route(req.Kind, routes)
	case "labels":
		cl, err := c.requireClient()
		if err != nil {
			return nil, err
		}
		labels, err := req.Labels.strs()
		if err != nil {
			return nil, err
		}
		return ok, cl.SetLabels(req.Kind, labels)
	case "locks":
		cl, err := c.requireClient()
		if err != nil {
			return nil, err
		}
		locks, err := req.Locks.strs()
		if err != nil {
			return nil, err
		}
		return ok, cl.SetLocks(req.Kind, locks)
	case "hubs/save":
		err := c.Store.SaveHub(strings.TrimSpace(req.Name), strings.TrimSpace(req.Host), req.Port)
		c.bump()
		return ok, err
	case "hubs/delete":
		err := c.Store.DeleteHub(req.Host, req.Port)
		c.bump()
		return ok, err
	case "theme":
		if req.Theme != "dark" && req.Theme != "light" {
			return nil, userErr("Thème inconnu : %s", req.Theme)
		}
		err := c.Store.SetTheme(req.Theme)
		c.bump()
		return ok, err
	case "salvos/save":
		return ok, c.saveSalvo(req.Name, req.Outputs)
	case "salvos/delete":
		cl, err := c.requireClient()
		if err != nil {
			return nil, err
		}
		err = c.Store.DeleteSalvo(hubKey(cl, cl.StateCopy()), req.Name)
		c.bump()
		return ok, err
	case "salvos/recall":
		skipped, err := c.recallSalvo(req.Name)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "skipped": skipped}, nil
	}
	return nil, userErr("Action inconnue : %s", path)
}

// indexes decodes "all" or a list of output indexes, bounded by size.
func indexes(raw json.RawMessage, size int) []int {
	var all string
	if json.Unmarshal(raw, &all) == nil && all == "all" {
		out := make([]int, size)
		for i := range out {
			out[i] = i
		}
		return out
	}
	var list []int
	json.Unmarshal(raw, &list)
	seen := map[int]bool{}
	out := []int{}
	for _, v := range list {
		if v >= 0 && v < size && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Ints(out)
	return out
}

func (c *Controller) saveSalvo(name string, outputs map[string]json.RawMessage) error {
	cl, err := c.requireClient()
	if err != nil {
		return err
	}
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return userErr("Nom de salve manquant")
	}
	st := cl.StateCopy()
	salvo := store.Salvo{Name: name, Video: [][2]int{}, Monitor: [][2]int{}}
	for _, o := range indexes(outputs["video"], len(st.Routing)) {
		salvo.Video = append(salvo.Video, [2]int{o, st.Routing[o]})
	}
	for _, o := range indexes(outputs["monitor"], len(st.MonitorRouting)) {
		salvo.Monitor = append(salvo.Monitor, [2]int{o, st.MonitorRouting[o]})
	}
	err = c.Store.SaveSalvo(hubKey(cl, st), salvo)
	c.bump()
	return err
}

func (c *Controller) recallSalvo(name string) (int, error) {
	cl, err := c.requireClient()
	if err != nil {
		return 0, err
	}
	st := cl.StateCopy()
	salvo, found := c.Store.GetSalvo(hubKey(cl, st), name)
	if !found {
		return 0, userErr("Salve introuvable")
	}
	skipped := 0
	for _, part := range []struct {
		kind   string
		routes [][2]int
		locks  []string
	}{{"video", salvo.Video, st.Locks}, {"monitor", salvo.Monitor, st.MonitorLocks}} {
		var routes []videohub.IntPair
		for _, r := range part.routes {
			if r[0] < 0 || r[0] >= len(part.locks) {
				continue
			}
			if part.locks[r[0]] == videohub.LockLocked {
				skipped++
				continue
			}
			routes = append(routes, r)
		}
		if err := cl.Route(part.kind, routes); err != nil {
			return skipped, err
		}
	}
	return skipped, nil
}

// IsUserFacing reports whether err carries a message meant for the UI.
func IsUserFacing(err error) bool {
	var ue *UserError
	var ce *videohub.CommandError
	return errors.As(err, &ue) || errors.As(err, &ce)
}
