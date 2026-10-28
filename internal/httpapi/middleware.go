package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

const headerRequestID = "X-Request-ID"

// ctxKey 是私有类型，避免与其他包的 context key 冲突。
type ctxKey struct{}

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(headerRequestID)
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set(headerRequestID, id)

		ctx := context.WithValue(r.Context(), ctxKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}

// StatusWriter 包一层 ResponseWriter，记录真正写出的状态码。
type StatusWriter struct {
	http.ResponseWriter // ← 嵌入：字段名就叫 ResponseWriter
	status              int
	wrote               bool
}

func (w *StatusWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.status = code
	w.wrote = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *StatusWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.status = http.StatusOK
		w.wrote = true
	}
	return w.ResponseWriter.Write(b)
}

func withLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &StatusWriter{ResponseWriter: w}

		next.ServeHTTP(sw, r)

		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", requestIDFrom(r.Context()),
			"remote", r.RemoteAddr,
		)
	})
}

func withRecover(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v) // 标准库的特殊约定，必须重新抛出
				}
				logger.Error("panic",
					"err", fmt.Sprint(v),
					"path", r.URL.Path,
					"request_id", requestIDFrom(r.Context()),
					"stack", string(debug.Stack()),
				)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
