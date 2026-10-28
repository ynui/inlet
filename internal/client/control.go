package client

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ynui/inlet/internal/proto"
)

type Conn struct {
	conn   *websocket.Conn
	send   chan []byte
	mu     sync.Mutex
	closed bool
}

func (c *Conn) write(mt int, b []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("closed")
	}
	c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.conn.WriteMessage(mt, b)
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
	conn, _, err := d.Dial(u.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	c := &Conn{conn: conn, send: make(chan []byte, 256)}
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
		return nil, nil, fmt.Errorf("auth failed")
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
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
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
	defer c.close()
	c.conn.SetReadLimit(512 * 1024)
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	for {
		_, msg, err := c.conn.ReadMessage()
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
