// Package store persists saved Videohubs and salvos as JSON in
// ~/.config/videohub-control/config.json (same file as the Python version).
package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Hub struct {
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
}

type Salvo struct {
	Name    string   `json:"name"`
	Video   [][2]int `json:"video"`
	Monitor [][2]int `json:"monitor"`
}

type LastHub struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type data struct {
	Hubs    []Hub              `json:"hubs"`
	Salvos  map[string][]Salvo `json:"salvos"`
	LastHub *LastHub           `json:"lastHub"`
	Theme   string             `json:"theme,omitempty"` // "dark" (default) or "light"
}

type Store struct {
	Path string
	mu   sync.Mutex
	d    data
}

func DefaultPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "videohub-control", "config.json")
}

func Open(path string) *Store {
	if path == "" {
		path = DefaultPath()
	}
	s := &Store{Path: path}
	if raw, err := os.ReadFile(path); err == nil {
		json.Unmarshal(raw, &s.d)
	}
	if s.d.Hubs == nil {
		s.d.Hubs = []Hub{}
	}
	if s.d.Salvos == nil {
		s.d.Salvos = map[string][]Salvo{}
	}
	return s
}

func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s.d, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

func lower(a, b string) bool { return strings.ToLower(a) < strings.ToLower(b) }

// --- hubs ------------------------------------------------------------------

func (s *Store) Hubs() []Hub {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Hub{}, s.d.Hubs...)
}

func (s *Store) SaveHub(name, host string, port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == "" {
		name = host
	}
	hubs := []Hub{}
	for _, h := range s.d.Hubs {
		if h.Host != host || h.Port != port {
			hubs = append(hubs, h)
		}
	}
	hubs = append(hubs, Hub{name, host, port})
	sort.Slice(hubs, func(i, j int) bool { return lower(hubs[i].Name, hubs[j].Name) })
	s.d.Hubs = hubs
	return s.save()
}

func (s *Store) DeleteHub(host string, port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	hubs := []Hub{}
	for _, h := range s.d.Hubs {
		if h.Host != host || h.Port != port {
			hubs = append(hubs, h)
		}
	}
	s.d.Hubs = hubs
	return s.save()
}

func (s *Store) LastHub() *LastHub {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.d.LastHub
}

func (s *Store) SetLastHub(host string, port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if host == "" {
		s.d.LastHub = nil
	} else {
		s.d.LastHub = &LastHub{host, port}
	}
	return s.save()
}

// --- theme -----------------------------------------------------------------

func (s *Store) Theme() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.d.Theme == "light" {
		return "light"
	}
	return "dark"
}

func (s *Store) SetTheme(theme string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Theme = theme
	return s.save()
}

// --- salvos (keyed by hub unique ID so they follow the device) -------------

func (s *Store) Salvos(hubKey string) []Salvo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Salvo{}, s.d.Salvos[hubKey]...)
}

func (s *Store) SaveSalvo(hubKey string, salvo Salvo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if salvo.Video == nil {
		salvo.Video = [][2]int{}
	}
	if salvo.Monitor == nil {
		salvo.Monitor = [][2]int{}
	}
	list := []Salvo{}
	for _, x := range s.d.Salvos[hubKey] {
		if x.Name != salvo.Name {
			list = append(list, x)
		}
	}
	list = append(list, salvo)
	sort.Slice(list, func(i, j int) bool { return lower(list[i].Name, list[j].Name) })
	s.d.Salvos[hubKey] = list
	return s.save()
}

func (s *Store) DeleteSalvo(hubKey, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := []Salvo{}
	for _, x := range s.d.Salvos[hubKey] {
		if x.Name != name {
			list = append(list, x)
		}
	}
	s.d.Salvos[hubKey] = list
	return s.save()
}

func (s *Store) GetSalvo(hubKey, name string) (Salvo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.d.Salvos[hubKey] {
		if x.Name == name {
			return x, true
		}
	}
	return Salvo{}, false
}
