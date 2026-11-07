package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"minidist/internal/kv"
	"minidist/internal/service"
)

// newTestHandler 每次返回一套全新的 handler + 空 store，用例之间互不影响。
func newTestHandler(t *testing.T) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(service.New(kv.NewStore()), logger, time.Second)
}

func TestReadyz(t *testing.T) {
	s := newTestHandler(t)

	if rec := do(s, "GET", "/readyz", ""); rec.Code != http.StatusOK {
		t.Fatalf("readyz = %d, want 200", rec.Code)
	}

	s.SetDraining()

	rec := do(s, "GET", "/readyz", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("draining readyz = %d, want 503", rec.Code)
	}
	var got errorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Error.Code != "draining" {
		t.Fatalf("code = %q", got.Error.Code)
	}
}

// do 发一个请求，返回 recorder。
func do(h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestStatusMatrix 只放【互相独立、不依赖顺序】的用例。
func TestStatusMatrix(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantCode   string // 空 = 不检查错误体
	}{
		{"put ok", "PUT", "/v1/kv/name", `{"value":"alice"}`, 204, ""},
		{"put empty value", "PUT", "/v1/kv/empty", `{"value":""}`, 204, ""},
		{"bad json", "PUT", "/v1/kv/name", `not-json`, 400, "bad_json"},
		{"unknown field", "PUT", "/v1/kv/name", `{"val":"x"}`, 400, "bad_json"},
		{"empty body", "PUT", "/v1/kv/name", ``, 400, "bad_json"},
		{"invalid key", "PUT", "/v1/kv/a%20b", `{"value":"x"}`, 400, "invalid_key"},
		{"get missing", "GET", "/v1/kv/nope", ``, 404, "not_found"},
		{"delete missing", "DELETE", "/v1/kv/nope", ``, 404, "not_found"},
		{"method not allowed", "POST", "/v1/kv/name", ``, 405, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(newTestHandler(t), tt.method, tt.path, tt.body)

			if rec.Code != tt.wantStatus {
				t.Fatalf("%s %s -> %d, want %d (body=%q)",
					tt.method, tt.path, rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantCode == "" {
				return
			}

			var got errorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("错误响应不是 JSON: %v (raw=%q)", err, rec.Body.String())
			}
			if got.Error.Code != tt.wantCode {
				t.Fatalf("error code = %q, want %q", got.Error.Code, tt.wantCode)
			}
			if got.Error.RequestID == "" {
				t.Fatal("错误响应缺少 request_id")
			}
		})
	}
}

// TestPutGetDelete 覆盖有顺序依赖的主流程。
func TestPutGetDelete(t *testing.T) {
	h := newTestHandler(t)

	if rec := do(h, "PUT", "/v1/kv/name", `{"value":"alice"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("PUT = %d (%q)", rec.Code, rec.Body.String())
	}

	rec := do(h, "GET", "/v1/kv/name", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var got kvResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("GET body 不是 JSON: %v", err)
	}
	if got.Key != "name" || got.Value != "alice" {
		t.Fatalf("GET = %+v, want key=name value=alice", got)
	}

	if rec := do(h, "PUT", "/v1/kv/name", `{"value":"bob"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("PUT overwrite = %d", rec.Code)
	}
	if body := do(h, "GET", "/v1/kv/name", "").Body.String(); !strings.Contains(body, `"bob"`) {
		t.Fatalf("覆盖后 GET = %q", body)
	}

	if rec := do(h, "DELETE", "/v1/kv/name", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d", rec.Code)
	}

	rec = do(h, "GET", "/v1/kv/name", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("删除后 GET = %d, want 404", rec.Code)
	}
	var errResp errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("404 body 不是 JSON: %v", err)
	}
	if errResp.Error.Code != "not_found" {
		t.Fatalf("404 code = %q", errResp.Error.Code)
	}
}

// TestBodyTooLarge 覆盖 413 的两条路径：
// A) body 本身超过读取上限；B) JSON 合法但 value 超过 service.MaxValueLen。
func TestBodyTooLarge(t *testing.T) {
	h := newTestHandler(t)

	huge := `{"value":"` + strings.Repeat("x", maxJSONBody) + `"}`
	if rec := do(h, "PUT", "/v1/kv/big", huge); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超大 body = %d, want 413", rec.Code)
	}

	big := `{"value":"` + strings.Repeat("x", service.MaxValueLen+1) + `"}`
	if rec := do(h, "PUT", "/v1/kv/big", big); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超大 value = %d, want 413", rec.Code)
	}
}

func TestHealthz(t *testing.T) {
	rec := do(newTestHandler(t), "GET", "/healthz", "")
	if rec.Code != http.StatusOK || rec.Body.String() != "OK" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestRequestID(t *testing.T) {
	t.Run("generated when absent", func(t *testing.T) {
		rec := do(newTestHandler(t), "GET", "/healthz", "")
		if rec.Header().Get("X-Request-Id") == "" {
			t.Fatal("缺少 X-Request-ID")
		}
	})

	t.Run("passed through when present", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Header.Set("X-Request-ID", "trace-001")
		rec := httptest.NewRecorder()
		newTestHandler(t).ServeHTTP(rec, req)
		if got := rec.Header().Get("X-Request-Id"); got != "trace-001" {
			t.Fatalf("got %q, want trace-001", got)
		}
	})

	t.Run("two requests get different ids", func(t *testing.T) {
		h := newTestHandler(t)
		ids := map[string]bool{}
		for i := 0; i < 10; i++ {
			ids[do(h, "GET", "/healthz", "").Header().Get("X-Request-Id")] = true
		}
		if len(ids) != 10 {
			t.Fatalf("期望 10 个不同的 ID，实际 %d 个", len(ids))
		}
	})
}
