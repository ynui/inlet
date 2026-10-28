package server

import (
	"crypto/rand"
	"encoding/hex"
	"sync"

	"github.com/ynui/inlet/internal/config"
)

type Assign struct {
	ID string `json:"id"`
}

type Store struct {
	mu   sync.RWMutex
	data map[string]Assign
}

func NewStore() *Store {
	s := &Store{data: map[string]Assign{}}
	_ = config.Load("assignments.json", &s.data)
	return s
}

func (s *Store) Get(token string) (Assign, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.data[token]
	return a, ok
}

func (s *Store) New(token string) Assign {
	id := randID(6)
	a := Assign{ID: id}
	s.mu.Lock()
	s.data[token] = a
	s.mu.Unlock()
	s.save()
	return a
}

func (s *Store) save() {
	s.mu.RLock()
	data := make(map[string]Assign, len(s.data))
	for k, v := range s.data {
		data[k] = v
	}
	s.mu.RUnlock()
	_ = config.Save("assignments.json", data)
}

func randID(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
