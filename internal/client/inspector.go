package client

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

//go:embed inspector.html
var inspectorHTML []byte

type Event struct {
	ID          string              `json:"id"`
	Method      string              `json:"method"`
	Path        string              `json:"path"`
	Status      int                 `json:"status"`
	Time        int64               `json:"time"`
	Size        int                 `json:"size"`
	At          int64               `json:"at"`
	Remote      string              `json:"remote,omitempty"`
	ReqHeaders  map[string][]string `json:"reqHeaders,omitempty"`
	ResHeaders  map[string][]string `json:"resHeaders,omitempty"`
	ReqBody     string              `json:"reqBody,omitempty"`
	BodyPreview string              `json:"bodyPreview,omitempty"`
}

type Inspector struct {
	mu      sync.RWMutex
	events  []Event
	public  string
	target  string
	total   int64
	status2 int64
	status3 int64
	status4 int64
	status5 int64
}

func NewInspector(public, target string) *Inspector {
	return &Inspector{
		public: public,
		target: target,
		events: []Event{},
	}
}

func (i *Inspector) SetPublic(pub string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.public = pub
}

func (i *Inspector) Public() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.public
}

func (i *Inspector) SetTarget(tgt string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.target = tgt
}

// Stats returns the total request count and the 2xx/3xx/4xx/5xx breakdown.
func (i *Inspector) Stats() (total, s2, s3, s4, s5 int64) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.total, i.status2, i.status3, i.status4, i.status5
}

func (i *Inspector) Events() []Event {
	i.mu.RLock()
	defer i.mu.RUnlock()
	ev := make([]Event, len(i.events))
	copy(ev, i.events)
	return ev
}

func (i *Inspector) Add(e Event) {
	e.At = time.Now().UnixMilli()
	i.mu.Lock()
	defer i.mu.Unlock()
	i.total++
	switch {
	case e.Status >= 200 && e.Status < 300:
		i.status2++
	case e.Status >= 300 && e.Status < 400:
		i.status3++
	case e.Status >= 400 && e.Status < 500:
		i.status4++
	case e.Status >= 500:
		i.status5++
	}
	i.events = append([]Event{e}, i.events...)
	if len(i.events) > 200 {
		i.events = i.events[:200]
	}
}

func (i *Inspector) Serve(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(inspectorHTML)
	})
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		i.mu.RLock()
		ev := make([]Event, len(i.events))
		copy(ev, i.events)
		pub := i.public
		tgt := i.target
		total := i.total
		s2 := i.status2
		s3 := i.status3
		s4 := i.status4
		s5 := i.status5
		i.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"public":  pub,
			"target":  tgt,
			"events":  ev,
			"total":   total,
			"status2": s2,
			"status3": s3,
			"status4": s4,
			"status5": s5,
		})
	})
	return http.ListenAndServe(addr, mux)
}
