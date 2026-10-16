// Package discovery is a minimal mDNS browser for Blackmagic devices
// (_blackmagic._tcp.local).
package discovery

import (
	"encoding/binary"
	"errors"
	"net"
	"sort"
	"strings"
	"time"
)

var (
	mdnsAddr = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}
	services = []string{"_blackmagic._tcp.local", "_videohub._tcp.local"}
)

const (
	typeA   = 1
	typePTR = 12
	typeTXT = 16
	typeSRV = 33
)

type Device struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Model    string `json:"model"`
	UniqueID string `json:"uniqueId"`
}

type srvRecord struct {
	target string
	port   uint16
}

type records struct {
	ptr map[string][]string
	srv map[string]srvRecord
	txt map[string]map[string]string
	a   map[string]string
}

func encodeName(name string) []byte {
	var out []byte
	for _, part := range strings.Split(strings.Trim(name, "."), ".") {
		out = append(out, byte(len(part)))
		out = append(out, part...)
	}
	return append(out, 0)
}

func buildQuery() []byte {
	q := make([]byte, 12)
	binary.BigEndian.PutUint16(q[4:], uint16(len(services)))
	for _, s := range services {
		q = append(q, encodeName(s)...)
		// QU bit (0x8000) asks responders to answer us directly by unicast.
		q = binary.BigEndian.AppendUint16(q, typePTR)
		q = binary.BigEndian.AppendUint16(q, 0x8001)
	}
	return q
}

var errShort = errors.New("short packet")

func readName(data []byte, off int) (string, int, error) {
	var labels []string
	end := -1
	for jumps := 0; jumps < 128; jumps++ { // guard against pointer loops
		if off >= len(data) {
			return "", 0, errShort
		}
		l := int(data[off])
		if l == 0 {
			off++
			if end < 0 {
				end = off
			}
			return strings.Join(labels, "."), end, nil
		}
		if l&0xC0 == 0xC0 {
			if off+1 >= len(data) {
				return "", 0, errShort
			}
			if end < 0 {
				end = off + 2
			}
			off = (l&0x3F)<<8 | int(data[off+1])
			continue
		}
		if off+1+l > len(data) {
			return "", 0, errShort
		}
		labels = append(labels, string(data[off+1:off+1+l]))
		off += 1 + l
	}
	return "", 0, errShort
}

func parse(data []byte, r *records) error {
	if len(data) < 12 {
		return errShort
	}
	if binary.BigEndian.Uint16(data[2:])&0x8000 == 0 {
		return nil // a query, not a response
	}
	qd := int(binary.BigEndian.Uint16(data[4:]))
	rr := int(binary.BigEndian.Uint16(data[6:])) + int(binary.BigEndian.Uint16(data[8:])) + int(binary.BigEndian.Uint16(data[10:]))
	off := 12
	for i := 0; i < qd; i++ {
		_, n, err := readName(data, off)
		if err != nil {
			return err
		}
		off = n + 4
	}
	for i := 0; i < rr; i++ {
		name, n, err := readName(data, off)
		if err != nil {
			return err
		}
		off = n
		if off+10 > len(data) {
			return errShort
		}
		rtype := binary.BigEndian.Uint16(data[off:])
		rdlen := int(binary.BigEndian.Uint16(data[off+8:]))
		rd := off + 10
		off = rd + rdlen
		if off > len(data) {
			return errShort
		}
		key := strings.ToLower(name)
		switch rtype {
		case typePTR:
			if target, _, err := readName(data, rd); err == nil {
				r.ptr[key] = append(r.ptr[key], target)
			}
		case typeSRV:
			if rdlen >= 7 {
				target, _, err := readName(data, rd+6)
				if err == nil {
					r.srv[key] = srvRecord{target, binary.BigEndian.Uint16(data[rd+4:])}
				}
			}
		case typeTXT:
			txt := map[string]string{}
			for p := rd; p < rd+rdlen; {
				l := int(data[p])
				if p+1+l > rd+rdlen {
					break
				}
				k, v, _ := strings.Cut(string(data[p+1:p+1+l]), "=")
				txt[strings.ToLower(k)] = v
				p += 1 + l
			}
			r.txt[key] = txt
		case typeA:
			if rdlen == 4 {
				r.a[key] = net.IP(data[rd : rd+4]).String()
			}
		}
	}
	return nil
}

// Discover browses the LAN for Videohubs. Best effort: errors yield an empty list.
func Discover(timeout time.Duration) []Device {
	r := &records{ptr: map[string][]string{}, srv: map[string]srvRecord{},
		txt: map[string]map[string]string{}, a: map[string]string{}}
	sources := map[string]string{} // instance → sender IP fallback

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return []Device{}
	}
	defer conn.Close()
	query := buildQuery()
	conn.WriteToUDP(query, mdnsAddr)
	start := time.Now()
	deadline := start.Add(timeout)
	resent := false
	buf := make([]byte, 9000)
	for time.Now().Before(deadline) {
		if !resent && time.Since(start) > timeout/2 {
			conn.WriteToUDP(query, mdnsAddr)
			resent = true
		}
		conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		before := map[string]bool{}
		for _, list := range r.ptr {
			for _, inst := range list {
				before[strings.ToLower(inst)] = true
			}
		}
		if parse(buf[:n], r) != nil {
			continue
		}
		for _, list := range r.ptr {
			for _, inst := range list {
				k := strings.ToLower(inst)
				if _, seen := sources[k]; !seen && !before[k] {
					sources[k] = from.IP.String()
				}
			}
		}
	}

	found := map[string]Device{}
	for _, svc := range services {
		for _, inst := range r.ptr[svc] {
			key := strings.ToLower(inst)
			txt := r.txt[key]
			host := r.a[strings.ToLower(r.srv[key].target)]
			if host == "" {
				host = sources[key]
			}
			if host == "" {
				continue
			}
			if _, dup := found[host]; dup {
				continue
			}
			if class := txt["class"]; class != "" && !strings.Contains(strings.ToLower(class), "videohub") {
				continue // another Blackmagic product (ATEM, HyperDeck...)
			}
			name := txt["name"]
			if name == "" {
				name, _, _ = strings.Cut(inst, "._")
			}
			model := txt["model"]
			if model == "" {
				model = txt["name"]
			}
			found[host] = Device{Name: name, Host: host, Model: model, UniqueID: txt["unique id"]}
		}
	}
	out := make([]Device, 0, len(found))
	for _, d := range found {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}
