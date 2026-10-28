package kv

import "sync"

// Store 是并发安全的内存KV
type Store struct {
	mu sync.RWMutex
	m map[string]string
}

func NewStore() *Store {
	return &Store{m: make(map[string]string)}
}

func (s *Store) Put(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = value
}

func (s *Store) Get (key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.m[key]
	return v, ok
}

func (s *Store) Delete(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[key]; !ok {
		return false
	}
	delete(s.m, key)
	return true;
}

func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.m)
}


		
