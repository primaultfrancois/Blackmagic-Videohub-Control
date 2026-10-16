package videohub

import (
	"reflect"
	"testing"
)

const prelude = "PROTOCOL PREAMBLE:\nVersion: 2.8\n\n" +
	"VIDEOHUB DEVICE:\nDevice present: true\nModel name: Smart Videohub 12G 40x40\n" +
	"Friendly name: Régie\nUnique ID: 7C2E0D021714\nVideo inputs: 4\nVideo processing units: 0\n" +
	"Video outputs: 3\nVideo monitoring outputs: 1\nSerial ports: 0\n\n" +
	"INPUT LABELS:\n0 Cam 1\n1 Cam 2\n2 Mac Pro\n3 Black\n\n" +
	"OUTPUT LABELS:\n0 PGM\n1 Multiview\n2 Rec\n\n" +
	"MONITORING OUTPUT LABELS:\n0 Check\n\n" +
	"VIDEO OUTPUT LOCKS:\n0 U\n1 L\n2 O\n\n" +
	"VIDEO OUTPUT ROUTING:\n0 2\n1 1\n2 0\n\n" +
	"VIDEO MONITORING OUTPUT ROUTING:\n0 3\n\n" +
	"VIDEO INPUT STATUS:\n0 BNC\n1 Optical\n\n" +
	"CONFIGURATION:\nTake Mode: true\n\n" +
	"END PRELUDE:\n\n"

func load(text string) *State {
	var p Parser
	s := NewState()
	for _, b := range p.Feed([]byte(text)) {
		s.Apply(b)
	}
	return s
}

func TestParserSplitAcrossChunks(t *testing.T) {
	var p Parser
	var blocks []Block
	data := []byte(prelude)
	for i := 0; i < len(data); i += 7 {
		blocks = append(blocks, p.Feed(data[i:min(i+7, len(data))])...)
	}
	if len(blocks) != 11 || blocks[10].Header != "END PRELUDE" {
		t.Fatalf("got %d blocks, last %q", len(blocks), blocks[len(blocks)-1].Header)
	}
}

func TestAckNakCRLF(t *testing.T) {
	var p Parser
	blocks := p.Feed([]byte("ACK\r\n\r\nNAK\n\n"))
	if len(blocks) != 2 || blocks[0].Header != "ACK" || blocks[1].Header != "NAK" {
		t.Fatalf("got %+v", blocks)
	}
}

func TestEncodeAndSanitize(t *testing.T) {
	if got := string(EncodeBlock("VIDEO OUTPUT ROUTING", []string{"5 3"})); got != "VIDEO OUTPUT ROUTING:\n5 3\n\n" {
		t.Fatalf("encode: %q", got)
	}
	if got := string(EncodeBlock("PING", nil)); got != "PING:\n\n" {
		t.Fatalf("ping: %q", got)
	}
	if got := SanitizeLabel("  Cam\n 1\r "); got != "Cam 1" {
		t.Fatalf("sanitize: %q", got)
	}
}

func TestPrelude(t *testing.T) {
	s := load(prelude)
	if s.ProtocolVersion != "2.8" || s.ModelName != "Smart Videohub 12G 40x40" || s.FriendlyName != "Régie" {
		t.Fatalf("device: %+v", s)
	}
	if s.VideoInputs != 4 || s.VideoOutputs != 3 || s.MonitoringOutputs != 1 {
		t.Fatalf("sizes: %d %d %d", s.VideoInputs, s.VideoOutputs, s.MonitoringOutputs)
	}
	check := func(name string, got, want any) {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	check("inputLabels", s.InputLabels, []string{"Cam 1", "Cam 2", "Mac Pro", "Black"})
	check("routing", s.Routing, []int{2, 1, 0})
	check("monitorRouting", s.MonitorRouting, []int{3})
	check("locks", s.Locks, []string{"U", "L", "O"})
	check("configuration", s.Configuration, map[string]string{"Take Mode": "true"})
	check("extra", s.Extra["VIDEO INPUT STATUS"], []string{"0 BNC", "1 Optical"})
}

func TestUpdatesAndBounds(t *testing.T) {
	s := load(prelude + "VIDEO OUTPUT ROUTING:\n1 3\n99 0\nbad line\n\nOUTPUT LABELS:\n2 Rec ISO 2\n\nVIDEO INPUT STATUS:\n1 BNC\n\n")
	if !reflect.DeepEqual(s.Routing, []int{2, 3, 0}) || s.OutputLabels[2] != "Rec ISO 2" {
		t.Fatalf("routing %v label %q", s.Routing, s.OutputLabels[2])
	}
	if !reflect.DeepEqual(s.Extra["VIDEO INPUT STATUS"], []string{"0 BNC", "1 BNC"}) {
		t.Fatalf("extra %v", s.Extra["VIDEO INPUT STATUS"])
	}
}

func TestDefaultsWhenLabelsMissing(t *testing.T) {
	s := load("VIDEOHUB DEVICE:\nVideo inputs: 2\nVideo outputs: 2\n\n")
	if !reflect.DeepEqual(s.InputLabels, []string{"Input 1", "Input 2"}) || !reflect.DeepEqual(s.Locks, []string{"U", "U"}) {
		t.Fatalf("defaults: %v %v", s.InputLabels, s.Locks)
	}
}
