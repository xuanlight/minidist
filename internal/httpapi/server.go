package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"runtime"

	"minidist/internal/kv"
)

const maxBody = 1 << 20 // 1 MB

type Server struct {
	store *kv.Store
	log   *slog.Logger
}

// New creates a new HTTP server with the given key-value store and logger.
func New(store *kv.Store, logger *slog.Logger) http.Handler {
	s := &Server{store: store, log: logger}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("PUT /kv/{key}", s.handlePut)
	mux.HandleFunc("GET /kv/{key}", s.handleGet)
	mux.HandleFunc("DELETE /kv/{key}", s.handleDelete)
	mux.HandleFunc("GET /stats", s.handleStats)

	return withRequestID(withLogging(logger, withRecover(logger, mux)))
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	// store.Get return string, bool
	key := r.PathValue("key")

	value, ok := s.store.Get(key)
	if !ok {
		http.Error(w, "key not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, value)
}

func (s *Server) handlePut(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")

	// 多读 1 字节，用来区分"刚好 1MB"和"超过 1MB"
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	if len(body) > maxBody {
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return
	}

	s.store.Put(key, string(body))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")

	if !s.store.Delete(key) {
		http.Error(w, "key not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"goroutines": runtime.NumGoroutine(),
		"keys":       s.store.Len(),
	})
}

