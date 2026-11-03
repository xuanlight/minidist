package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"minidist/internal/service"
)

type errorResponse struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// writeJSON 是所有 JSON 响应的唯一出口：设 Content-Type、写状态行、编码 body。
func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// fail 用于传输层自己发现的错误（坏 JSON、body 过大等）。
func (s *Server) fail(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	if status >= 500 {
		s.log.Error("request failed",
			"code", code, "msg", msg, "path", r.URL.Path,
			"request_id", requestIDFrom(r.Context()))
	}
	s.writeJSON(w, status, errorResponse{Error: errorDetail{
		Code:      code,
		Message:   msg,
		RequestID: requestIDFrom(r.Context()),
	}})
}

// writeServiceError 是领域错误 → HTTP 状态码的唯一映射点。
func (s *Server) writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		s.fail(w, r, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, service.ErrInvalidKey):
		s.fail(w, r, http.StatusBadRequest, "invalid_key", err.Error())
	case errors.Is(err, service.ErrTooLarge):
		s.fail(w, r, http.StatusRequestEntityTooLarge, "too_large", err.Error())
	default:
		// 500 的细节只进日志，不回给客户端
		s.fail(w, r, http.StatusInternalServerError, "internal", "internal error")
	}
}
