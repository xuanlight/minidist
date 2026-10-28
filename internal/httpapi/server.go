package httpapi

import (
	"log/slog"
	"net/http"
	
	"minidist/internal/kv"
)

const maxBody = 1 << 20 // 1 MB

type Server struct {
	store *kv.Store
	log *slog.Logger
}

// New creates a new HTTP server with the given key-value store and logger.
func New(store *kv.Store, logger *slog.Logger) http.Handler {
	s := &Server{ store: store, log: logger }
	
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

func (s *Server) handlePut(w http.ResponseWriter, r *http.Request) {
	// implementation for handling PUT requests to store a key-value pair
	key := r.PathValue("key")

	// one more byte check for the body size
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	if Len(body) > maxBody {
		http.Error(w, "body too Large", http.StatusRequestEntityTooLarge)
		return 
	}

	s.store.Put(key, body)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	// store.Get return string, bool
	key := r.PathValue("key")

	value, ok := s.store.Get(key)
	if !ok {
		http.Error(w, "key not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = io.WriteString(w, value)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	// store delete return bool
	key := r.PathValue("key")

	_, ok := s.store.Delete(key)
	if !ok {
		http.Error(w, "key not found", http.StatusNotFound)
		return
	}
	
	w.WriteHeader(http.StatusNoContent)
}
