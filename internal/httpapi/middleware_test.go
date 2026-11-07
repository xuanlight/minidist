package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"minidist/internal/kv"
	"minidist/internal/service"
)

func TestTimeoutCancelsContext(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(service.New(kv.NewStore()), logger, 50*time.Millisecond)

	var canceled bool
	h := s.withTimeout(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // 只等取消信号，什么都不做
		canceled = true
	}))

	done := make(chan struct{})
	go func() {
		h.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "/", nil))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler 没有如期返回：超时信号没传下去")
	}

	if !canceled {
		t.Fatal("ctx 没有被取消")
	}
}
