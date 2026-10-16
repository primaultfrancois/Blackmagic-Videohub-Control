package videohub

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	pingInterval = 10 * time.Second
	ackTimeout   = 3 * time.Second
)

// CommandError is returned when a command can't be sent or the hub refuses it.
type CommandError struct{ Msg string }

func (e *CommandError) Error() string { return e.Msg }

func cmdErr(format string, a ...any) error { return &CommandError{fmt.Sprintf(format, a...)} }

// ConnSnapshot is the connection state as seen by the UI.
type ConnSnapshot struct {
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Status string `json:"status"`
	Error  string `json:"error"`
	Hub    *State `json:"hub"`
}

// Client is one connection to one Videohub. OnChange is called (from the
// reader goroutine) whenever hub state or connection status changes.
type Client struct {
	Host     string
	Port     int
	OnChange func()

	mu     sync.Mutex // guards state, status, errMsg, conn
	state  *State
	status string // disconnected | connecting | connected
	errMsg string
	conn   net.Conn

	sendMu          sync.Mutex // serializes command + ACK wait
	acks            chan string
	pingOutstanding atomic.Bool

	stop chan struct{}
	done chan struct{}
}

func NewClient(host string, port int, onChange func()) *Client {
	if onChange == nil {
		onChange = func() {}
	}
	return &Client{Host: host, Port: port, OnChange: onChange, state: NewState(),
		status: "disconnected", acks: make(chan string, 16)}
}

func (c *Client) Start() {
	c.stop = make(chan struct{})
	c.done = make(chan struct{})
	go c.run()
}

func (c *Client) Stop() {
	if c.stop == nil {
		return
	}
	close(c.stop)
	c.mu.Lock()
	if c.conn != nil {
		c.conn.Close()
	}
	c.mu.Unlock()
	select {
	case <-c.done:
	case <-time.After(3 * time.Second):
	}
	c.stop = nil
	c.setStatus("disconnected", "")
}

func (c *Client) stopped() bool {
	select {
	case <-c.stop:
		return true
	default:
		return false
	}
}

func (c *Client) Status() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// StateCopy returns a deep copy of the current hub state.
func (c *Client) StateCopy() *State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state.Clone()
}

func (c *Client) Snapshot() ConnSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := ConnSnapshot{Host: c.Host, Port: c.Port, Status: c.status, Error: c.errMsg}
	if c.status == "connected" {
		s.Hub = c.state.Clone()
	}
	return s
}

func (c *Client) setStatus(status, errMsg string) {
	c.mu.Lock()
	if c.status == status && c.errMsg == errMsg {
		c.mu.Unlock()
		return
	}
	c.status, c.errMsg = status, errMsg
	c.mu.Unlock()
	c.OnChange()
}

func (c *Client) wait(d time.Duration) {
	select {
	case <-c.stop:
	case <-time.After(d):
	}
}

func (c *Client) run() {
	defer close(c.done)
	backoff := time.Second
	addr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	for !c.stopped() {
		c.mu.Lock()
		prevErr := c.errMsg
		c.mu.Unlock()
		c.setStatus("connecting", prevErr)
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			c.setStatus("connecting", "Connexion impossible : "+describe(err))
			c.wait(backoff)
			backoff = min(backoff*2, 10*time.Second)
			continue
		}
		backoff = time.Second
		if tcp, ok := conn.(*net.TCPConn); ok {
			tcp.SetKeepAlive(true)
		}
		c.mu.Lock()
		c.conn = conn
		c.state = NewState()
		c.mu.Unlock()
		c.pingOutstanding.Store(false)
		err = c.readLoop(conn)
		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()
		conn.Close()
		if !c.stopped() {
			c.setStatus("connecting", "Connexion perdue : "+describe(err))
			c.wait(time.Second)
		}
	}
}

func describe(err error) string {
	var op *net.OpError
	if errors.As(err, &op) && op.Err != nil {
		return op.Err.Error()
	}
	return err.Error()
}

func (c *Client) readLoop(conn net.Conn) error {
	var parser Parser
	buf := make([]byte, 65536)
	lastRx := time.Now()
	var pingSent time.Time
	for !c.stopped() {
		conn.SetReadDeadline(time.Now().Add(time.Second))
		n, err := conn.Read(buf)
		now := time.Now()
		if n > 0 {
			lastRx, pingSent = now, time.Time{}
			c.handle(parser.Feed(buf[:n]))
		}
		if err == nil {
			continue
		}
		var ne net.Error
		if !errors.As(err, &ne) || !ne.Timeout() {
			if errors.Is(err, io.EOF) {
				return errors.New("fermée par le Videohub")
			}
			return err
		}
		switch {
		case !pingSent.IsZero() && now.Sub(pingSent) > pingInterval:
			return errors.New("pas de réponse au ping")
		case pingSent.IsZero() && now.Sub(lastRx) > pingInterval && c.Status() == "connected":
			// Skip if a command is in flight; its ACK proves liveness.
			if c.sendMu.TryLock() {
				c.pingOutstanding.Store(true)
				conn.SetWriteDeadline(now.Add(ackTimeout))
				_, werr := conn.Write(EncodeBlock("PING", nil))
				c.sendMu.Unlock()
				if werr != nil {
					return werr
				}
				pingSent = now
			}
		}
	}
	return nil
}

func (c *Client) handle(blocks []Block) {
	changed := false
	for _, b := range blocks {
		if b.Header == "ACK" || b.Header == "NAK" {
			// Replies arrive in order, so the first one after a ping is its.
			if !c.pingOutstanding.CompareAndSwap(true, false) {
				select {
				case c.acks <- b.Header:
				default:
				}
			}
			continue
		}
		c.mu.Lock()
		if c.state.Apply(b) {
			changed = true
		}
		c.mu.Unlock()
		if b.Header == "END PRELUDE" {
			c.setStatus("connected", "")
			changed = true
		}
	}
	if changed && c.Status() == "connected" {
		c.OnChange()
	}
}

// Send writes one command block and waits for the hub's ACK.
func (c *Client) Send(header string, lines []string) error {
	c.mu.Lock()
	conn, status := c.conn, c.status
	c.mu.Unlock()
	if status != "connected" || conn == nil {
		return cmdErr("Videohub non connecté")
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	for len(c.acks) > 0 {
		<-c.acks
	}
	conn.SetWriteDeadline(time.Now().Add(ackTimeout))
	if _, err := conn.Write(EncodeBlock(header, lines)); err != nil {
		return cmdErr("Envoi impossible : %v", describe(err))
	}
	select {
	case reply := <-c.acks:
		if reply != "ACK" {
			return cmdErr("Commande refusée par le Videohub (NAK)")
		}
		return nil
	case <-time.After(ackTimeout):
		return cmdErr("Pas de réponse du Videohub")
	}
}

type IntPair = [2]int

type LabelPair struct {
	Index int
	Text  string
}

func (c *Client) Route(kind string, routes []IntPair) error {
	header, ok := RoutingBlocks[kind]
	if !ok {
		return cmdErr("Type de routage inconnu : %s", kind)
	}
	if len(routes) == 0 {
		return nil
	}
	lines := make([]string, len(routes))
	for i, r := range routes {
		lines[i] = fmt.Sprintf("%d %d", r[0], r[1])
	}
	return c.Send(header, lines)
}

func (c *Client) SetLabels(kind string, labels []LabelPair) error {
	header, ok := LabelBlocks[kind]
	if !ok {
		return cmdErr("Type d'étiquette inconnu : %s", kind)
	}
	if len(labels) == 0 {
		return nil
	}
	lines := make([]string, len(labels))
	for i, l := range labels {
		lines[i] = fmt.Sprintf("%d %s", l.Index, SanitizeLabel(l.Text))
	}
	return c.Send(header, lines)
}

func (c *Client) SetLocks(kind string, locks []LabelPair) error {
	header, ok := LockBlocks[kind]
	if !ok {
		return cmdErr("Type de verrou inconnu : %s", kind)
	}
	for _, l := range locks {
		if l.Text != "O" && l.Text != "U" && l.Text != "F" {
			return cmdErr("État de verrou invalide : %s", l.Text)
		}
	}
	if len(locks) == 0 {
		return nil
	}
	lines := make([]string, len(locks))
	for i, l := range locks {
		lines[i] = fmt.Sprintf("%d %s", l.Index, l.Text)
	}
	return c.Send(header, lines)
}
