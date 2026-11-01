package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"

	"minidist/internal/kv"
)

const maxBody = 1 << 20 // 1 MB

type Server struct {
	store    *kv.Store
	log      *slog.Logger
	requests atomic.Int64
	start    time.Time
}

// New 组装路由和中间件链。
func New(store *kv.Store, logger *slog.Logger) http.Handler {
	s := &Server{store: store, log: logger, start: time.Now()}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("PUT /kv/{key}", s.handlePut)
	mux.HandleFunc("GET /kv/{key}", s.handleGet)
	mux.HandleFunc("DELETE /kv/{key}", s.handleDelete)
	mux.HandleFunc("GET /stats", s.handleStats)

	// 从外到内：requestID → logging → recover → mux
	return s.withRequestID(s.withLogging(s.withRecover(mux)))
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "OK")
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
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
	resp := struct {
		Goroutines    int   `json:"goroutines"`
		Requests      int64 `json:"requests"`
		Keys          int   `json:"keys"`
		UptimeSeconds int64 `json:"uptime_seconds"`
	}{
		Goroutines:    runtime.NumGoroutine(),
		Requests:      s.requests.Load(),
		Keys:          s.store.Len(),
		UptimeSeconds: int64(time.Since(s.start).Seconds()),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
