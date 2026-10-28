package kv

import "testing"

func TestStore(t *testing.T) {
	s := NewStore()
	if _, ok := s.Get("a"); ok { t.Fatal("want miss") }
	s.Put("a", "1")
	if v, ok := s.Get("a"); !ok || v != "1" { t.Fatal("Got %q %v", v, ok) }
	if !s.Delete("a") { t.Fatal("delete want true") }
	if s.Delete("a") { t.Fatal("delete again want false") }
}

