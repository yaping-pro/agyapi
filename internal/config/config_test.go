package config

import (
	"flag"
	"os"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	// Clear relevant env vars
	os.Unsetenv("PORT")
	os.Unsetenv("HOST")
	os.Unsetenv("AGY_PATH")
	os.Unsetenv("DEFAULT_MODEL")
	os.Unsetenv("MAX_CONCURRENCY")
	os.Unsetenv("VERBOSE")

	cfg := Load()
	if cfg.Port != 7862 {
		t.Errorf("expected default Port 7862, got %d", cfg.Port)
	}
	if cfg.Host != "127.0.0.1" {
		t.Errorf("expected default Host 127.0.0.1, got %s", cfg.Host)
	}
	if cfg.AgyPath == "" {
		t.Errorf("expected default AgyPath to be non-empty, got %s", cfg.AgyPath)
	}
	if cfg.DefaultModel != "gemini-3.8-flash-high" {
		t.Errorf("expected default DefaultModel 'gemini-3.8-flash-high', got %s", cfg.DefaultModel)
	}
	if cfg.MaxConcurrency != 4 {
		t.Errorf("expected default MaxConcurrency 4, got %d", cfg.MaxConcurrency)
	}
	if cfg.Verbose != false {
		t.Errorf("expected default Verbose false, got %v", cfg.Verbose)
	}
}

func TestLoad_EnvOverrides(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("HOST", "0.0.0.0")
	t.Setenv("AGY_PATH", "/opt/bin/agy")
	t.Setenv("DEFAULT_MODEL", "claude-sonnet-4-6")
	t.Setenv("MAX_CONCURRENCY", "10")
	t.Setenv("VERBOSE", "true")

	cfg := Load()
	if cfg.Port != 9090 {
		t.Errorf("expected Port 9090, got %d", cfg.Port)
	}
	if cfg.Host != "0.0.0.0" {
		t.Errorf("expected Host 0.0.0.0, got %s", cfg.Host)
	}
	if cfg.AgyPath != "/opt/bin/agy" {
		t.Errorf("expected AgyPath '/opt/bin/agy', got %s", cfg.AgyPath)
	}
	if cfg.DefaultModel != "claude-sonnet-4-6" {
		t.Errorf("expected DefaultModel 'claude-sonnet-4-6', got %s", cfg.DefaultModel)
	}
	if cfg.MaxConcurrency != 10 {
		t.Errorf("expected MaxConcurrency 10, got %d", cfg.MaxConcurrency)
	}
	if !cfg.Verbose {
		t.Errorf("expected Verbose true, got %v", cfg.Verbose)
	}
}

func TestConfig_BindFlags(t *testing.T) {
	cfg := &Config{
		Port:           8000,
		Host:           "127.0.0.1",
		AgyPath:        "/opt/bin/agy",
		DefaultModel:   "gemini-3.8-flash-high",
		MaxConcurrency: 4,
		Verbose:        false,
	}

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cfg.BindFlags(fs)

	args := []string{
		"-port", "9999",
		"-host", "192.168.1.50",
		"-agy-path", "/usr/local/bin/agy",
		"-default-model", "gemini-3.7-flash-high",
		"-max-concurrency", "16",
		"-verbose",
	}

	if err := fs.Parse(args); err != nil {
		t.Fatalf("failed to parse flags: %v", err)
	}

	if cfg.Port != 9999 {
		t.Errorf("expected Port 9999, got %d", cfg.Port)
	}
	if cfg.Host != "192.168.1.50" {
		t.Errorf("expected Host 192.168.1.50, got %s", cfg.Host)
	}
	if cfg.AgyPath != "/usr/local/bin/agy" {
		t.Errorf("expected AgyPath '/usr/local/bin/agy', got %s", cfg.AgyPath)
	}
	if cfg.DefaultModel != "gemini-3.7-flash-high" {
		t.Errorf("expected DefaultModel 'gemini-3.7-flash-high', got %s", cfg.DefaultModel)
	}
	if cfg.MaxConcurrency != 16 {
		t.Errorf("expected MaxConcurrency 16, got %d", cfg.MaxConcurrency)
	}
	if !cfg.Verbose {
		t.Errorf("expected Verbose true, got %v", cfg.Verbose)
	}
}

func TestLoad_SanitizeMaxConcurrency(t *testing.T) {
	t.Setenv("MAX_CONCURRENCY", "0")
	cfg := Load()
	if cfg.MaxConcurrency != 4 {
		t.Errorf("expected sanitized MaxConcurrency 4 for '0', got %d", cfg.MaxConcurrency)
	}

	t.Setenv("MAX_CONCURRENCY", "-10")
	cfg = Load()
	if cfg.MaxConcurrency != 4 {
		t.Errorf("expected sanitized MaxConcurrency 4 for '-10', got %d", cfg.MaxConcurrency)
	}
}
