// Package videohub implements the Blackmagic Videohub Ethernet Protocol
// (TCP 9990): block parsing, the hub state model and a reconnecting client.
//
// The protocol is line based. Messages are "blocks": a header line ending
// with a colon, zero or more body lines, then an empty line. ACK / NAK are
// blocks without a colon. All indexes are 0-based on the wire.
package videohub

import (
	"fmt"
	"strconv"
	"strings"
)

const DefaultPort = 9990

// Lock states as reported by the device.
const (
	LockOwned    = "O" // locked by this client
	LockLocked   = "L" // locked by another client
	LockUnlocked = "U"
	LockForce    = "F" // command only: force unlock
)

var (
	RoutingBlocks = map[string]string{"video": "VIDEO OUTPUT ROUTING", "monitor": "VIDEO MONITORING OUTPUT ROUTING"}
	LockBlocks    = map[string]string{"video": "VIDEO OUTPUT LOCKS", "monitor": "MONITORING OUTPUT LOCKS"}
	LabelBlocks   = map[string]string{"input": "INPUT LABELS", "output": "OUTPUT LABELS", "monitor": "MONITORING OUTPUT LABELS"}
)

type Block struct {
	Header string
	Lines  []string
}

// Parser is an incremental block parser: feed raw bytes, get complete blocks.
type Parser struct{ buf string }

func (p *Parser) Feed(data []byte) []Block {
	p.buf += strings.ReplaceAll(string(data), "\r\n", "\n")
	var blocks []Block
	for {
		// Skip stray blank lines between blocks.
		p.buf = strings.TrimLeft(p.buf, "\n")
		end := strings.Index(p.buf, "\n\n")
		if end < 0 {
			return blocks
		}
		raw := p.buf[:end]
		p.buf = p.buf[end+2:]
		lines := strings.Split(raw, "\n")
		header := strings.TrimSuffix(strings.TrimSpace(lines[0]), ":")
		blocks = append(blocks, Block{Header: header, Lines: lines[1:]})
	}
}

func EncodeBlock(header string, lines []string) []byte {
	var b strings.Builder
	b.WriteString(header + ":\n")
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	b.WriteString("\n")
	return []byte(b.String())
}

// SanitizeLabel collapses whitespace (including newlines) so a label can't
// break the line-based protocol.
func SanitizeLabel(label string) string {
	return strings.Join(strings.Fields(label), " ")
}

func splitIndex(line string) (int, string, bool) {
	head, rest, _ := strings.Cut(line, " ")
	i, err := strconv.Atoi(head)
	if err != nil {
		return 0, "", false
	}
	return i, rest, true
}

// State mirrors everything the hub reports. JSON names match the web UI.
type State struct {
	ProtocolVersion   string              `json:"protocolVersion"`
	DevicePresent     string              `json:"devicePresent"`
	ModelName         string              `json:"modelName"`
	FriendlyName      string              `json:"friendlyName"`
	UniqueID          string              `json:"uniqueId"`
	VideoInputs       int                 `json:"videoInputs"`
	VideoOutputs      int                 `json:"videoOutputs"`
	MonitoringOutputs int                 `json:"monitoringOutputs"`
	ProcessingUnits   int                 `json:"processingUnits"`
	SerialPorts       int                 `json:"serialPorts"`
	InputLabels       []string            `json:"inputLabels"`
	OutputLabels      []string            `json:"outputLabels"`
	MonitorLabels     []string            `json:"monitorLabels"`
	Routing           []int               `json:"routing"`
	MonitorRouting    []int               `json:"monitorRouting"`
	Locks             []string            `json:"locks"`
	MonitorLocks      []string            `json:"monitorLocks"`
	Configuration     map[string]string   `json:"configuration"`
	Extra             map[string][]string `json:"extra"` // blocks we don't model, kept raw
}

func NewState() *State {
	s := &State{Configuration: map[string]string{}, Extra: map[string][]string{}}
	s.resize()
	return s
}

func fit[T any](list []T, n int, fill func(int) T) []T {
	out := make([]T, n)
	copy(out, list)
	for i := len(list); i < n; i++ {
		out[i] = fill(i)
	}
	return out
}

func (s *State) resize() {
	label := func(prefix string) func(int) string {
		return func(i int) string { return fmt.Sprintf("%s %d", prefix, i+1) }
	}
	zero := func(int) int { return 0 }
	unlocked := func(int) string { return LockUnlocked }
	s.InputLabels = fit(s.InputLabels, s.VideoInputs, label("Input"))
	s.OutputLabels = fit(s.OutputLabels, s.VideoOutputs, label("Output"))
	s.MonitorLabels = fit(s.MonitorLabels, s.MonitoringOutputs, label("Monitor"))
	s.Routing = fit(s.Routing, s.VideoOutputs, zero)
	s.MonitorRouting = fit(s.MonitorRouting, s.MonitoringOutputs, zero)
	s.Locks = fit(s.Locks, s.VideoOutputs, unlocked)
	s.MonitorLocks = fit(s.MonitorLocks, s.MonitoringOutputs, unlocked)
}

// Apply updates state from a status block. Returns true if state changed.
func (s *State) Apply(b Block) bool {
	switch b.Header {
	case "PROTOCOL PREAMBLE":
		for _, line := range b.Lines {
			if k, v, _ := strings.Cut(line, ":"); strings.TrimSpace(k) == "Version" {
				s.ProtocolVersion = strings.TrimSpace(v)
			}
		}
		return true
	case "VIDEOHUB DEVICE":
		s.applyDevice(b.Lines)
		return true
	case "CONFIGURATION":
		for _, line := range b.Lines {
			if k, v, _ := strings.Cut(line, ":"); strings.TrimSpace(k) != "" {
				s.Configuration[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
		return true
	case "INPUT LABELS":
		applyLabels(s.InputLabels, b.Lines)
		return true
	case "OUTPUT LABELS":
		applyLabels(s.OutputLabels, b.Lines)
		return true
	case "MONITORING OUTPUT LABELS":
		applyLabels(s.MonitorLabels, b.Lines)
		return true
	case "VIDEO OUTPUT ROUTING":
		applyRouting(s.Routing, b.Lines)
		return true
	case "VIDEO MONITORING OUTPUT ROUTING":
		applyRouting(s.MonitorRouting, b.Lines)
		return true
	case "VIDEO OUTPUT LOCKS":
		applyLocks(s.Locks, b.Lines)
		return true
	case "MONITORING OUTPUT LOCKS":
		applyLocks(s.MonitorLocks, b.Lines)
		return true
	case "ACK", "NAK", "END PRELUDE", "PING":
		return false
	}
	if len(b.Lines) == 0 {
		return true
	}
	// Unmodelled blocks (serial ports, processing units, alarms, status...):
	// keep the latest value per leading key, in arrival order.
	lines := s.Extra[b.Header]
	for _, line := range b.Lines {
		key, _, _ := strings.Cut(line, " ")
		replaced := false
		for i, old := range lines {
			if k, _, _ := strings.Cut(old, " "); k == key {
				lines[i], replaced = strings.TrimRight(line, " "), true
				break
			}
		}
		if !replaced {
			lines = append(lines, strings.TrimRight(line, " "))
		}
	}
	s.Extra[b.Header] = lines
	return true
}

func applyLabels(target []string, lines []string) {
	for _, line := range lines {
		if i, rest, ok := splitIndex(line); ok && i >= 0 && i < len(target) {
			target[i] = rest
		}
	}
}

func applyRouting(target []int, lines []string) {
	for _, line := range lines {
		i, rest, ok := splitIndex(line)
		if !ok {
			continue
		}
		src, err := strconv.Atoi(strings.TrimSpace(rest))
		if err == nil && i >= 0 && i < len(target) {
			target[i] = src
		}
	}
}

func applyLocks(target []string, lines []string) {
	for _, line := range lines {
		if i, rest, ok := splitIndex(line); ok && i >= 0 && i < len(target) {
			v := strings.TrimSpace(rest)
			if v == "" {
				v = LockUnlocked
			}
			target[i] = v[:1]
		}
	}
}

func (s *State) applyDevice(lines []string) {
	for _, line := range lines {
		k, v, _ := strings.Cut(line, ":")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		n, nerr := strconv.Atoi(v)
		setInt := func(dst *int) {
			if nerr == nil {
				*dst = n
			}
		}
		switch k {
		case "Video inputs":
			setInt(&s.VideoInputs)
		case "Video outputs":
			setInt(&s.VideoOutputs)
		case "Video monitoring outputs":
			setInt(&s.MonitoringOutputs)
		case "Video processing units":
			setInt(&s.ProcessingUnits)
		case "Serial ports":
			setInt(&s.SerialPorts)
		case "Device present":
			s.DevicePresent = v
		case "Model name":
			s.ModelName = v
		case "Friendly name":
			s.FriendlyName = v
		case "Unique ID":
			s.UniqueID = v
		}
	}
	s.resize()
}

// Clone returns a deep copy, safe to hand to another goroutine.
func (s *State) Clone() *State {
	c := *s
	c.InputLabels = append([]string(nil), s.InputLabels...)
	c.OutputLabels = append([]string(nil), s.OutputLabels...)
	c.MonitorLabels = append([]string(nil), s.MonitorLabels...)
	c.Routing = append([]int(nil), s.Routing...)
	c.MonitorRouting = append([]int(nil), s.MonitorRouting...)
	c.Locks = append([]string(nil), s.Locks...)
	c.MonitorLocks = append([]string(nil), s.MonitorLocks...)
	c.Configuration = make(map[string]string, len(s.Configuration))
	for k, v := range s.Configuration {
		c.Configuration[k] = v
	}
	c.Extra = make(map[string][]string, len(s.Extra))
	for k, v := range s.Extra {
		c.Extra[k] = append([]string(nil), v...)
	}
	// JSON must carry [] rather than null for empty lists.
	for _, l := range []*[]string{&c.InputLabels, &c.OutputLabels, &c.MonitorLabels, &c.Locks, &c.MonitorLocks} {
		if *l == nil {
			*l = []string{}
		}
	}
	for _, l := range []*[]int{&c.Routing, &c.MonitorRouting} {
		if *l == nil {
			*l = []int{}
		}
	}
	return &c
}
