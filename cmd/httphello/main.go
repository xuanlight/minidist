package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"minidist/internal/config"
	"minidist/internal/httpapi"
	"minidist/internal/kv"
	"minidist/internal/service"
)

func main() {
	cfg, err := config.Load(os.Args[1:], os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(2)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))

	// 装配：store（数据）→ service（业务）→ httpapi（传输）
	store := kv.NewStore()
	svc := service.New(store)
	api := httpapi.New(svc, logger, cfg.RequestTimeout)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api,
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

	logger.Info("server started", "addr", cfg.Addr)

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("listen", "err", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		stop()
		logger.Info("shutting down")

		// 先让 readiness 失败，等负载均衡把流量摘掉，再真正关闭。
		api.SetDraining()
		time.Sleep(2 * time.Second)

		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown timeout, force close", "err", err)
			_ = srv.Close()
		}
		logger.Info("bye")
	}
}
