package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ynui/inlet/internal/proto"
)

// ErrAuth is returned by Connect when the server rejects the token.
var ErrAuth = errors.New("auth failed")

type Conn struct {
	conn   *websocket.Conn
	send   chan []byte
	mu     sync.Mutex
	closed bool
	done   chan struct{}
}

func (c *Conn) write(mt int, b []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("closed")
	}
	c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return proto.WriteFull(c.conn, mt, b)
}

func (c *Conn) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	c.conn.Close()
}

func Connect(serverURL, token string, newURL bool, localPort int, ins *Inspector) (*Conn, *proto.Registered, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return nil, nil, err
	}
	if u.Path == "" {
		u.Path = "/_inlet/control"
	}
	d := websocket.DefaultDialer
	conn, resp, err := d.Dial(u.String(), nil)
	if err != nil {
		if resp == nil {
			return nil, nil, fmt.Errorf("connect to control server: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		detail := strings.TrimSpace(string(body))
		if detail != "" {
			return nil, nil, fmt.Errorf("WebSocket handshake failed: HTTP %s: %s", resp.Status, detail)
		}
		return nil, nil, fmt.Errorf("WebSocket handshake failed: HTTP %s", resp.Status)
	}
	c := &Conn{conn: conn, send: make(chan []byte, 256), done: make(chan struct{})}
	// auth
	a := proto.Auth{Token: token, New: newURL}
	b, _ := proto.Marshal(proto.TypeAuth, a)
	if err := c.write(websocket.TextMessage, b); err != nil {
		conn.Close()
		return nil, nil, err
	}
	// read registered
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	var m proto.Message
	if err := json.Unmarshal(msg, &m); err != nil {
		conn.Close()
		return nil, nil, err
	}
	if m.Type == proto.TypeError {
		conn.Close()
		msg := "unauthorized"
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(m.Data, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		return nil, nil, fmt.Errorf("%w: %s", ErrAuth, msg)
	}
	if m.Type != proto.TypeRegistered {
		conn.Close()
		return nil, nil, fmt.Errorf("bad response")
	}
	reg, err := proto.UnmarshalRegistered(m.Data)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	go c.writePump()
	go c.readPump(localPort, ins)
	return c, reg, nil
}

func (c *Conn) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case msg := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := proto.WriteFull(c.conn, websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *Conn) readPump(localPort int, ins *Inspector) {
	defer func() {
		c.close()
		close(c.done)
	}()
	c.conn.SetReadLimit(512 * 1024)
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	for {
		msg, err := proto.ReadFull(c.conn)
		if err != nil {
			break
		}
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		var m proto.Message
		if err := json.Unmarshal(msg, &m); err != nil {
			continue
		}
		if m.Type == proto.TypeReq {
			go handleReq(c, m.Data, localPort, ins)
		}
	}
}

func (c *Conn) Send(b []byte) {
	select {
	case c.send <- b:
	default:
	}
}

func (c *Conn) Done() <-chan struct{} { return c.done }

func (c *Conn) Close() { c.close() }
