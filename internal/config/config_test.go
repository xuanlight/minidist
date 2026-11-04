package config_test

import (
	"log/slog"
	"testing"
	"time"

	"minidist/internal/config"
)

func fakeEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		env       map[string]string
		wantAddr  string
		wantLevel slog.Level
		wantErr   bool
	}{
		{"defaults", nil, nil, ":8080", slog.LevelInfo, false},
		{"env addr", nil,
			map[string]string{"MINIDIST_ADDR": ":9001"}, ":9001", slog.LevelInfo, false},
		{"flag beats env", []string{"-addr=:9002"},
			map[string]string{"MINIDIST_ADDR": ":9001"}, ":9002", slog.LevelInfo, false},
		{"log level from env", nil,
			map[string]string{"MINIDIST_LOG_LEVEL": "DEBUG"}, ":8080", slog.LevelDebug, false},
		{"log level from flag", []string{"-log-level=error"},
			map[string]string{"MINIDIST_LOG_LEVEL": "debug"}, ":8080", slog.LevelError, false},
		{"bad level", []string{"-log-level=loud"}, nil, "", 0, true},
		{"bad duration", []string{"-request-timeout=-1s"}, nil, "", 0, true},
		{"empty addr", []string{"-addr="}, nil, "", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := config.Load(tt.args, fakeEnv(tt.env))
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if cfg.Addr != tt.wantAddr {
				t.Fatalf("Addr = %q, want %q", cfg.Addr, tt.wantAddr)
			}
			if cfg.LogLevel != tt.wantLevel {
				t.Fatalf("LogLevel = %v, want %v", cfg.LogLevel, tt.wantLevel)
			}
		})
	}
}

func TestLoadDurations(t *testing.T) {
	cfg, err := config.Load([]string{"-request-timeout=1500ms", "-shutdown-grace=2s"}, fakeEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RequestTimeout != 1500*time.Millisecond {
		t.Fatalf("RequestTimeout = %v", cfg.RequestTimeout)
	}
	if cfg.ShutdownGrace != 2*time.Second {
		t.Fatalf("ShutdownGrace = %v", cfg.ShutdownGrace)
	}
}

func TestLoadDefaultsDurations(t *testing.T) {
	cfg, err := config.Load(nil, fakeEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RequestTimeout != 3*time.Second || cfg.ShutdownGrace != 5*time.Second {
		t.Fatalf("durations = %v / %v", cfg.RequestTimeout, cfg.ShutdownGrace)
	}
}
