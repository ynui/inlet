package proto

import "encoding/json"

type MsgType string

const (
	TypeAuth       MsgType = "auth"
	TypeRegistered MsgType = "registered"
	TypeReq        MsgType = "req"
	TypeRes        MsgType = "res"
	TypeError      MsgType = "error"
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
