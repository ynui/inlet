package auth

import "sync"

type Store struct {
	mu     sync.RWMutex
	tokens map[string]bool
}

func New(tokens []string) *Store {
	m := map[string]bool{}
	for _, t := range tokens {
		if t != "" {
			m[t] = true
		}
	}
	return &Store{tokens: m}
}

func (s *Store) Valid(t string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tokens[t]
}
