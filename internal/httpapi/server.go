package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"

	"minidist/internal/service"
)

type Server struct {
	svc      *service.Service
	log      *slog.Logger
	requests atomic.Int64
	start    time.Time
	draining atomic.Bool
	h        http.Handler
}

// New 组装路由和中间件链。
func New(svc *service.Service, logger *slog.Logger) *Server {
	s := &Server{svc: svc, log: logger, start: time.Now()}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("PUT /v1/kv/{key}", s.handlePut)
	mux.HandleFunc("GET /v1/kv/{key}", s.handleGet)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	mux.HandleFunc("DELETE /v1/kv/{key}", s.handleDelete)
	mux.HandleFunc("GET /stats", s.handleStats)

	// 从外到内：requestID → logging → recover → mux
	s.h = s.withRequestID(s.withLogging(s.withRecover(mux)))
	return s
}

// ServeHTTP 让 *Server 自己就是一个 http.Handler：
// 实际工作交给 New 里组装好的中间件链。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.h.ServeHTTP(w, r)
}

// SetDraining 标记服务进入关停流程，readiness 随即失败。
func (s *Server) SetDraining() { s.draining.Store(true) }

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if s.draining.Load() {
		s.fail(w, r, http.StatusServiceUnavailable, "draining", "shutting down")
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "OK")
}

type kvResponse struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")

	value, err := s.svc.Get(key)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, kvResponse{Key: key, Value: value})
}

const maxJSONBody = service.MaxValueLen + 4096 // JSON 包装（引号、转义、键名）留点余量

type putRequest struct {
	Value string `json:"value"`
}

func (s *Server) handlePut(w http.ResponseWriter, r *http.Request) {
	// 1) 传输层保护：别把内存读爆（多读 1 字节用来判断超限）
	body, err := io.ReadAll(io.LimitReader(r.Body, maxJSONBody+1))
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "read_body", "cannot read body")
		return
	}
	if len(body) > maxJSONBody {
		s.fail(w, r, http.StatusRequestEntityTooLarge, "too_large", "body too large")
		return
	}

	// 2) JSON 解析：严格模式，拼错的字段会报错而不是被静默忽略
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	var req putRequest
	if err := dec.Decode(&req); err != nil {
		s.fail(w, r, http.StatusBadRequest, "bad_json", "invalid JSON body: "+err.Error())
		return
	}

	// 3) 业务校验交给 service
	if err := s.svc.Put(r.PathValue("key"), req.Value); err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Delete(r.PathValue("key")); err != nil {
		s.writeServiceError(w, r, err)
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
		Keys:          s.svc.Len(),
		UptimeSeconds: int64(time.Since(s.start).Seconds()),
	}

	s.writeJSON(w, http.StatusOK, resp)
}
