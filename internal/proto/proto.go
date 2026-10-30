package proto

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/gorilla/websocket"
)

type MsgType string

const (
	TypeAuth       MsgType = "auth"
	TypeRegistered MsgType = "registered"
	TypeReq        MsgType = "req"
	TypeRes        MsgType = "res"
	TypeError      MsgType = "error"
	TypeFragment   MsgType = "fragment"
)

type Message struct {
	Type MsgType         `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

type Auth struct {
	Token string `json:"token"`
	New   bool   `json:"new,omitempty"`
}

type Registered struct {
	ID        string `json:"id"`
	PublicURL string `json:"publicUrl"`
}

type Request struct {
	ID      string              `json:"id"`
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body,omitempty"`
	Remote  string              `json:"remote,omitempty"`
}

type Response struct {
	ID      string              `json:"id"`
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    []byte              `json:"body,omitempty"`
	Final   bool                `json:"final,omitempty"`
}

func Marshal(t MsgType, v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	m := Message{Type: t, Data: data}
	return json.Marshal(m)
}

func UnmarshalAuth(data json.RawMessage) (*Auth, error) {
	var a Auth
	err := json.Unmarshal(data, &a)
	return &a, err
}

func UnmarshalRegistered(data json.RawMessage) (*Registered, error) {
	var r Registered
	err := json.Unmarshal(data, &r)
	return &r, err
}

func UnmarshalRequest(data json.RawMessage) (*Request, error) {
	var r Request
	err := json.Unmarshal(data, &r)
	return &r, err
}

func UnmarshalResponse(data json.RawMessage) (*Response, error) {
	var r Response
	err := json.Unmarshal(data, &r)
	return &r, err
}

type Fragment struct {
	ID    string `json:"id"`
	Seq   int    `json:"seq"`
	Final bool   `json:"final"`
	Data  []byte `json:"data,omitempty"`
}

func UnmarshalFragment(data json.RawMessage) (*Fragment, error) {
	var f Fragment
	err := json.Unmarshal(data, &f)
	return &f, err
}

const fragmentThreshold = 64 * 1024

var fragCounter uint64
var fragMu sync.Mutex

func nextFragID() string {
	fragMu.Lock()
	defer fragMu.Unlock()
	fragCounter++
	return fmt.Sprintf("f%d", fragCounter)
}

func WriteFull(conn *websocket.Conn, mt int, b []byte) error {
	if len(b) <= fragmentThreshold {
		return conn.WriteMessage(mt, b)
	}
	id := nextFragID()
	chunkSize := 32 * 1024
	seq := 0
	for off := 0; off < len(b); off += chunkSize {
		end := off + chunkSize
		if end > len(b) {
			end = len(b)
		}
		final := end == len(b)
		frag := Fragment{ID: id, Seq: seq, Final: final, Data: b[off:end]}
		fb, err := Marshal(TypeFragment, frag)
		if err != nil {
			return err
		}
		if err := conn.WriteMessage(websocket.TextMessage, fb); err != nil {
			return err
		}
		seq++
	}
	return nil
}

func ReadFull(conn *websocket.Conn) ([]byte, error) {
	var acc []byte
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		var m Message
		if err := json.Unmarshal(msg, &m); err == nil && m.Type == TypeFragment {
			f, err := UnmarshalFragment(m.Data)
			if err != nil {
				return nil, err
			}
			acc = append(acc, f.Data...)
			if f.Final {
				return acc, nil
			}
			continue
		}
		return msg, nil
	}
}
