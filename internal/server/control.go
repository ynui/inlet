package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ynui/inlet/internal/proto"
	"github.com/ynui/inlet/internal/store"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		o := r.Header.Get("Origin")
		if o == "" {
			return true
		}
		u, err := url.Parse(o)
		if err != nil {
			return false
		}
		return strings.EqualFold(u.Host, r.Host)
	},
}

type Client struct {
	id          string
	conn        *websocket.Conn
	mu          sync.Mutex
	closed      bool
	connectedAt time.Time
}

func (c *Client) write(mt int, b []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("closed")
	}
	c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return proto.WriteFull(c.conn, mt, b)
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
	store      *store.Store
	public     string
	publicPort string
	mu         sync.RWMutex
	clients    map[string]*Client
	respMu     sync.RWMutex
	resp       map[string]chan *proto.Response
}

func NewHub(s *store.Store) *Hub {
	return &Hub{
		store:   s,
		clients: map[string]*Client{},
		resp:    map[string]chan *proto.Response{},
	}
}

// SetPublicBase is the externally reachable base URL (e.g. https://example.com),
// advertised to clients so they can print a working tunnel URL.
func (h *Hub) SetPublicBase(base string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.public = strings.TrimRight(base, "/")
}

// SetPublicAddr records the public listener address so the derived URL points at
// the public port, not the control port the registration arrived on.
func (h *Hub) SetPublicAddr(addr string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, port, err := net.SplitHostPort(addr); err == nil {
		h.publicPort = port
	}
}

func (h *Hub) PublicURL(id string, r *http.Request) string {
	h.mu.RLock()
	public := h.public
	publicPort := h.publicPort
	h.mu.RUnlock()
	if public != "" {
		return public + "/" + id
	}
	scheme := "http"
	forwardedProto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if r.TLS != nil || strings.EqualFold(forwardedProto, "https") {
		scheme = "https"
	}
	host := r.Host
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
		host = fh
	} else if publicPort != "" && forwardedProto == "" {
		if name, _, err := net.SplitHostPort(host); err == nil && name != "" {
			host = net.JoinHostPort(name, publicPort)
		} else if host != "" && !strings.Contains(host, ":") {
			host = net.JoinHostPort(host, publicPort)
		}
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host + "/" + id
}

func (h *Hub) Online(id string) bool { return h.getClient(id) != nil }

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
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			log.Printf("control upgrade rejected remote=%q error=%q", r.RemoteAddr, err)
		}
		return
	}
	c := &Client{conn: conn}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		log.Printf("control authentication failed remote=%q error=%q", r.RemoteAddr, err)
		conn.Close()
		return
	}
	var m proto.Message
	if err := json.Unmarshal(msg, &m); err != nil || m.Type != proto.TypeAuth {
		log.Printf("control authentication failed remote=%q reason=invalid_auth_message", r.RemoteAddr)
		conn.Close()
		return
	}
	a, err := proto.UnmarshalAuth(m.Data)
	if err != nil || a == nil {
		log.Printf("control authentication failed remote=%q reason=malformed_auth", r.RemoteAddr)
		conn.Close()
		return
	}
	tok, err := h.store.Validate(a.Token)
	if err != nil {
		log.Printf("control authentication rejected remote=%q reason=invalid_or_revoked_token", r.RemoteAddr)
		b, _ := proto.Marshal(proto.TypeError, map[string]string{"error": "invalid or revoked token"})
		conn.WriteMessage(websocket.TextMessage, b)
		conn.Close()
		return
	}
	if a.New {
		tid, err := h.store.RotateTunnel(tok.ID)
		if err != nil {
			log.Printf("control tunnel rotation failed tunnel=%s error=%q", tok.TunnelID, err)
			conn.Close()
			return
		}
		tok.TunnelID = tid
	}
	_ = h.store.Touch(tok.ID)

	asn := proto.Registered{ID: tok.TunnelID, PublicURL: h.PublicURL(tok.TunnelID, r)}
	c.id = asn.ID
	h.register(c.id, c)

	b, _ := proto.Marshal(proto.TypeRegistered, asn)
	if err := c.write(websocket.TextMessage, b); err != nil {
		h.unregister(c.id, c)
		log.Printf("control registration failed tunnel=%s error=%q", c.id, err)
		conn.Close()
		return
	}

	c.connectedAt = time.Now()
	log.Printf("control client connected tunnel=%s", c.id)
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
		log.Printf("control client disconnected tunnel=%s duration=%s", c.id, time.Since(c.connectedAt).Round(time.Second))
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
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Printf("control read failed tunnel=%s error=%q", c.id, err)
			}
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

func (h *Hub) sendReq(id, tunnelID string, req *proto.Request) (<-chan *proto.Response, error) {
	ch := make(chan *proto.Response, 16)
	h.respMu.Lock()
	h.resp[id] = ch
	h.respMu.Unlock()

	c := h.getClient(tunnelID)
	if c == nil {
		h.respMu.Lock()
		delete(h.resp, id)
		h.respMu.Unlock()
		return nil, fmt.Errorf("tunnel offline")
	}
	b, err := proto.Marshal(proto.TypeReq, req)
	if err != nil {
		h.respMu.Lock()
		delete(h.resp, id)
		h.respMu.Unlock()
		return nil, err
	}
	if err := c.write(websocket.TextMessage, b); err != nil {
		h.respMu.Lock()
		delete(h.resp, id)
		h.respMu.Unlock()
		return nil, err
	}
	return ch, nil
}
