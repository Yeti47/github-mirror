// Package config loads and validates runtime configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all runtime configuration for the application.
type Config struct {
	// GitHubToken is the read-only personal access token for GitHub API and git auth.
	GitHubToken string
	// MirrorDir is the root directory where bare mirror clones are stored.
	MirrorDir string
	// SyncInterval is how often a full sync cycle is performed.
	SyncInterval time.Duration
	// Concurrency is the number of repositories synced in parallel.
	Concurrency int
	// IncludeLFS controls whether Git LFS objects are fetched for each repo.
	IncludeLFS bool
	// GitTimeout is the maximum duration allowed for a single git operation.
	GitTimeout time.Duration
	// DBPath is the filesystem path to the SQLite database that stores sync
	// protocols (used by the health check).
	DBPath string
	// LogLevel controls the minimum log level ("debug", "info", "warn", "error").
	LogLevel string
}

// Load reads configuration from environment variables and returns a validated Config.
// It returns an error if any required variable is missing or any value is invalid.
func Load() (*Config, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return nil, errors.New("GITHUB_TOKEN environment variable is required")
	}

	syncInterval, err := envDurationOrDefault("SYNC_INTERVAL", time.Hour)
	if err != nil {
		return nil, fmt.Errorf("invalid SYNC_INTERVAL: %w", err)
	}

	concurrency, err := envIntOrDefault("CONCURRENCY", 4)
	if err != nil {
		return nil, fmt.Errorf("invalid CONCURRENCY: %w", err)
	}
	if concurrency < 1 {
		return nil, errors.New("CONCURRENCY must be at least 1")
	}

	includeLFS, err := envBoolOrDefault("INCLUDE_LFS", true)
	if err != nil {
		return nil, fmt.Errorf("invalid INCLUDE_LFS: %w", err)
	}

	gitTimeout, err := envDurationOrDefault("GIT_TIMEOUT", 30*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("invalid GIT_TIMEOUT: %w", err)
	}

	return &Config{
		GitHubToken:  token,
		MirrorDir:    envStringOrDefault("MIRROR_DIR", "/data"),
		SyncInterval: syncInterval,
		Concurrency:  concurrency,
		IncludeLFS:   includeLFS,
		GitTimeout:   gitTimeout,
		DBPath:       envStringOrDefault("DB_PATH", "/var/lib/github-mirror/state.db"),
		LogLevel:     envStringOrDefault("LOG_LEVEL", "info"),
	}, nil
}

func envStringOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDurationOrDefault(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	return time.ParseDuration(v)
}

func envIntOrDefault(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	return strconv.Atoi(v)
}

func envBoolOrDefault(key string, def bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	return strconv.ParseBool(v)
}
