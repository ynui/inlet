package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ynui/inlet/internal/auth"
	"github.com/ynui/inlet/internal/proto"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Client struct {
	id     string
	conn   *websocket.Conn
	mu     sync.Mutex
	closed bool
}

func (c *Client) write(mt int, b []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("closed")
	}
	c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.conn.WriteMessage(mt, b)
}

func (c *Client) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	c.conn.Close()
}

type Hub struct {
	auth    *auth.Store
	assign  *Store
	public  string
	mu      sync.RWMutex
	clients map[string]*Client
	respMu  sync.RWMutex
	resp    map[string]chan *proto.Response
}

func NewHub(a *auth.Store, as *Store) *Hub {
	return &Hub{
		auth:    a,
		assign:  as,
		clients: map[string]*Client{},
		resp:    map[string]chan *proto.Response{},
	}
}

// SetPublicBase is the externally reachable base URL (e.g. https://example.com),
// advertised to clients so they can print a working tunnel URL.
func (h *Hub) SetPublicBase(base string) {
	h.public = strings.TrimRight(base, "/")
}

func (h *Hub) getClient(id string) *Client {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.clients[id]
}

func (h *Hub) register(id string, c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[id] = c
}

func (h *Hub) unregister(id string, c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[id] == c {
		delete(h.clients, id)
	}
}

func (h *Hub) HandleControl(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &Client{conn: conn}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return
	}
	var m proto.Message
	if err := json.Unmarshal(msg, &m); err != nil || m.Type != proto.TypeAuth {
		conn.Close()
		return
	}
	a, err := proto.UnmarshalAuth(m.Data)
	if err != nil || a == nil || !h.auth.Valid(a.Token) {
		b, _ := proto.Marshal(proto.TypeError, map[string]string{"error": "unauthorized"})
		conn.WriteMessage(websocket.TextMessage, b)
		conn.Close()
		return
	}

	var asn proto.Registered
	ass, ok := h.assign.Get(a.Token)
	if a.New || !ok {
		ass = h.assign.New(a.Token)
	}
	asn.ID = ass.ID
	if h.public != "" {
		asn.PublicURL = h.public + "/" + ass.ID
	}
	c.id = ass.ID
	h.register(c.id, c)

	b, _ := proto.Marshal(proto.TypeRegistered, asn)
	if err := c.write(websocket.TextMessage, b); err != nil {
		h.unregister(c.id, c)
		conn.Close()
		return
	}

	go c.pingLoop()
	c.readPump(h)
}

func (c *Client) pingLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if err := c.write(websocket.PingMessage, nil); err != nil {
			c.close()
			return
		}
	}
}

func (c *Client) readPump(h *Hub) {
	defer func() {
		h.unregister(c.id, c)
		c.close()
	}()
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
		h.route(msg)
	}
}

func (h *Hub) route(msg []byte) {
	var m proto.Message
	if err := json.Unmarshal(msg, &m); err != nil || m.Type != proto.TypeRes {
		return
	}
	var res proto.Response
	if err := json.Unmarshal(m.Data, &res); err != nil {
		return
	}
	h.respMu.RLock()
	ch := h.resp[res.ID]
	h.respMu.RUnlock()
	if ch != nil {
		ch <- &res
	}
}

func (h *Hub) sendReq(id, tunnelID string, req *proto.Request) (*proto.Response, error) {
	ch := make(chan *proto.Response, 1)
	h.respMu.Lock()
	h.resp[id] = ch
	h.respMu.Unlock()
	defer func() {
		h.respMu.Lock()
		delete(h.resp, id)
		h.respMu.Unlock()
	}()

	c := h.getClient(tunnelID)
	if c == nil {
		return nil, fmt.Errorf("tunnel offline")
	}
	b, err := proto.Marshal(proto.TypeReq, req)
	if err != nil {
		return nil, err
	}
	if err := c.write(websocket.TextMessage, b); err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		return r, nil
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("timeout")
	}
}
