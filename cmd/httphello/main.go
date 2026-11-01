package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"minidist/internal/httpapi"
	"minidist/internal/kv"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		//	Level: slog.LevelInfo,
		Level: slog.LevelInfo,
	}))

	store := kv.NewStore()
	handler := httpapi.New(store, logger)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,  // 防 Slowloris
		ReadTimeout:       10 * time.Second, // 读整个请求的上限
		WriteTimeout:      10 * time.Second, // 写响应的上限
		IdleTimeout:       60 * time.Second, // keep-alive 空闲
	}

	// 收到 SIGINT / SIGTERM 时 ctx 被取消
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	logger.Info("server started", "addr", *addr)

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("listen", "err", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		stop() // 恢复默认信号处理：第二次 Ctrl-C 直接强杀，不会卡住

		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown timeout, force close", "err", err)
			_ = srv.Close()
		}
		logger.Info("bye")
	}
}
