package config_test

import (
	"testing"
	"time"

	"github.com/Yeti47/github-mirror/internal/config"
)

func TestLoad_RequiresToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when GITHUB_TOKEN is empty, got nil")
	}
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_test")
	t.Setenv("SYNC_INTERVAL", "")
	t.Setenv("CONCURRENCY", "")
	t.Setenv("INCLUDE_LFS", "")
	t.Setenv("GIT_TIMEOUT", "")
	t.Setenv("MIRROR_DIR", "")
	t.Setenv("DB_PATH", "")
	t.Setenv("LOG_LEVEL", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.GitHubToken != "ghp_test" {
		t.Errorf("GitHubToken = %q, want %q", cfg.GitHubToken, "ghp_test")
	}
	if cfg.SyncInterval != time.Hour {
		t.Errorf("SyncInterval = %v, want %v", cfg.SyncInterval, time.Hour)
	}
	if cfg.Concurrency != 4 {
		t.Errorf("Concurrency = %d, want 4", cfg.Concurrency)
	}
	if !cfg.IncludeLFS {
		t.Error("IncludeLFS = false, want true")
	}
	if cfg.GitTimeout != 30*time.Minute {
		t.Errorf("GitTimeout = %v, want 30m", cfg.GitTimeout)
	}
	if cfg.MirrorDir != "/data" {
		t.Errorf("MirrorDir = %q, want %q", cfg.MirrorDir, "/data")
	}
	if cfg.DBPath != "/var/lib/github-mirror/state.db" {
		t.Errorf("DBPath = %q, want default", cfg.DBPath)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
}

func TestLoad_CustomValues(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_custom")
	t.Setenv("SYNC_INTERVAL", "30m")
	t.Setenv("CONCURRENCY", "8")
	t.Setenv("INCLUDE_LFS", "false")
	t.Setenv("GIT_TIMEOUT", "1h")
	t.Setenv("MIRROR_DIR", "/mnt/backup")
	t.Setenv("DB_PATH", "/srv/state.db")
	t.Setenv("LOG_LEVEL", "debug")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.SyncInterval != 30*time.Minute {
		t.Errorf("SyncInterval = %v, want 30m", cfg.SyncInterval)
	}
	if cfg.Concurrency != 8 {
		t.Errorf("Concurrency = %d, want 8", cfg.Concurrency)
	}
	if cfg.IncludeLFS {
		t.Error("IncludeLFS = true, want false")
	}
	if cfg.GitTimeout != time.Hour {
		t.Errorf("GitTimeout = %v, want 1h", cfg.GitTimeout)
	}
	if cfg.MirrorDir != "/mnt/backup" {
		t.Errorf("MirrorDir = %q, want /mnt/backup", cfg.MirrorDir)
	}
	if cfg.DBPath != "/srv/state.db" {
		t.Errorf("DBPath = %q, want /srv/state.db", cfg.DBPath)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug", cfg.LogLevel)
	}
}

func TestLoad_InvalidValues(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
	}{
		{
			name: "invalid SYNC_INTERVAL",
			env:  map[string]string{"SYNC_INTERVAL": "not-a-duration"},
		},
		{
			name: "invalid CONCURRENCY",
			env:  map[string]string{"CONCURRENCY": "abc"},
		},
		{
			name: "zero CONCURRENCY",
			env:  map[string]string{"CONCURRENCY": "0"},
		},
		{
			name: "invalid INCLUDE_LFS",
			env:  map[string]string{"INCLUDE_LFS": "maybe"},
		},
		{
			name: "invalid GIT_TIMEOUT",
			env:  map[string]string{"GIT_TIMEOUT": "∞"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Set valid base values.
			t.Setenv("GITHUB_TOKEN", "ghp_test")
			t.Setenv("SYNC_INTERVAL", "")
			t.Setenv("CONCURRENCY", "")
			t.Setenv("INCLUDE_LFS", "")
			t.Setenv("GIT_TIMEOUT", "")

			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			_, err := config.Load()
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}
