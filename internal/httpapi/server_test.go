package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"minidist/internal/kv"
)

// newTestHandler 创建一个日志静音的新 handler，以及它背后的全新 store。
func newTestHandler(t *testing.T) (http.Handler, *kv.Store) {
	t.Helper()
	store := kv.NewStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(store, logger), store
}

func TestKV(t *testing.T) {
	type step struct {
		method     string
		path       string
		body       string
		wantStatus int
		wantBody   string
	}
	tests := []struct {
		name  string
		steps []step
	}{
		{
			name: "put then get",
			steps: []step{
				{http.MethodPut, "/kv/name", "alice", http.StatusNoContent, ""},
				{http.MethodGet, "/kv/name", "", http.StatusOK, "alice"},
			},
		},
		{
			name: "put overwrite",
			steps: []step{
				{http.MethodPut, "/kv/k", "v1", http.StatusNoContent, ""},
				{http.MethodPut, "/kv/k", "v2", http.StatusNoContent, ""},
				{http.MethodGet, "/kv/k", "", http.StatusOK, "v2"},
			},
		},
		{
			name: "delete then get",
			steps: []step{
				{http.MethodPut, "/kv/k", "v", http.StatusNoContent, ""},
				{http.MethodDelete, "/kv/k", "", http.StatusNoContent, ""},
				{http.MethodGet, "/kv/k", "", http.StatusNotFound, "key not found\n"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := newTestHandler(t)
			for i, s := range tt.steps {
				req := httptest.NewRequest(s.method, s.path, strings.NewReader(s.body))
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)

				if rec.Code != s.wantStatus {
					t.Fatalf("step %d: %s %s -> status %d, want %d (body=%q)",
						i, s.method, s.path, rec.Code, s.wantStatus, rec.Body.String())
				}
				if s.wantBody != "" && rec.Body.String() != s.wantBody {
					t.Fatalf("step %d: %s %s -> body %q, want %q",
						i, s.method, s.path, rec.Body.String(), s.wantBody)
				}
			}
		})
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h, _ := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/kv/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if rec.Header().Get("Allow") == "" {
		t.Fatal("405 必须带 Allow 头")
	}
}

func TestBodyTooLarge(t *testing.T) {
	h, _ := newTestHandler(t)
	body := strings.Repeat("x", maxBody+1)
	req := httptest.NewRequest(http.MethodPut, "/kv/big", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestHealthz(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "OK" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestRequestID(t *testing.T) {
	t.Run("generated when absent", func(t *testing.T) {
		h, _ := newTestHandler(t)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if rec.Header().Get("X-Request-Id") == "" {
			t.Fatal("缺少 X-Request-ID")
		}
	})

	t.Run("passed through when present", func(t *testing.T) {
		h, _ := newTestHandler(t)
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Header.Set("X-Request-ID", "trace-001")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("X-Request-Id"); got != "trace-001" {
			t.Fatalf("got %q, want trace-001", got)
		}
	})

	t.Run("two requests get different ids", func(t *testing.T) {
		h, _ := newTestHandler(t)
		ids := map[string]bool{}
		for i := 0; i < 10; i++ {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
			ids[rec.Header().Get("X-Request-Id")] = true
		}
		if len(ids) != 10 {
			t.Fatalf("期望 10 个不同的 ID，实际 %d 个", len(ids))
		}
	})
}
