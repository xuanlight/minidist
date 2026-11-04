package config

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

const (
	// DefaultAddr 默认监听地址。
	DefaultAddr = ":8080"
	// DefaultRequestTimeout
	DefaultRequestTimeout = 3 * time.Second
	// DefaultShutdownGrace
	DefaultShutdownGrace = 5 * time.Second
)

type Config struct {
	Addr           string
	LogLevel       slog.Level
	RequestTimeout time.Duration
	ShutdownGrace  time.Duration
}

// Load 解析命令行参数和环境变量，返回配置。
func Load(args []string, getenv func(string) string) (Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	addr := envOr(getenv, "MINIDIST_ADDR", DefaultAddr)
	logLevel := envOr(getenv, "MINIDIST_LOG_LEVEL", "info")
	requestTimeout := envOr(getenv, "MINIDIST_REQUEST_TIMEOUT", DefaultRequestTimeout.String())
	shutdownGrace := envOr(getenv, "MINIDIST_SHUTDOWN_GRACE", DefaultShutdownGrace.String())

	flags := flag.NewFlagSet("minidist", flag.ContinueOnError)
	flags.StringVar(&addr, "addr", addr, "listen address")
	flags.StringVar(&logLevel, "log-level", logLevel, "log level")
	flags.StringVar(&requestTimeout, "request-timeout", requestTimeout, "request timeout")
	flags.StringVar(&shutdownGrace, "shutdown-grace", shutdownGrace, "shutdown grace period")
	if err := flags.Parse(args); err != nil {
		return Config{}, fmt.Errorf("parse flags: %w", err)
	}

	if strings.TrimSpace(addr) == "" {
		return Config{}, errors.New("addr must not be empty")
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.ToUpper(logLevel))); err != nil {
		return Config{}, fmt.Errorf("invalid log level %q: %w", logLevel, err)
	}

	requestTimeoutValue, err := parsePositiveDuration("request timeout", requestTimeout)
	if err != nil {
		return Config{}, err
	}
	shutdownGraceValue, err := parsePositiveDuration("shutdown grace", shutdownGrace)
	if err != nil {
		return Config{}, err
	}

	return Config{
		Addr:           addr,
		LogLevel:       level,
		RequestTimeout: requestTimeoutValue,
		ShutdownGrace:  shutdownGraceValue,
	}, nil
}

func envOr(getenv func(string) string, key, fallback string) string {
	if value := getenv(key); value != "" {
		return value
	}
	return fallback
}

func parsePositiveDuration(name, value string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", name, value, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}
	return duration, nil
}
