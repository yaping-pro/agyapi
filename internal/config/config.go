package config

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	Port           int
	Host           string
	DefaultModel   string
	MaxConcurrency int
	Verbose        bool
	// TokenPath points at the Antigravity OAuth token JSON.
	TokenPath string
	// Project is the cloudaicompanionProject used in the envelope.
	Project string
	// UpstreamURL is the Cloud Code Assist base URL.
	UpstreamURL string
	// OAuthTokenURL is the Google token endpoint used for refresh.
	OAuthTokenURL string
	// OAuthClientID / OAuthClientSecret identify the Antigravity CLI
	// installed-app OAuth client.
	OAuthClientID     string
	OAuthClientSecret string
}

// BindFlags registers the configuration flags on the given FlagSet.
func (c *Config) BindFlags(fs *flag.FlagSet) {
	fs.IntVar(&c.Port, "port", c.Port, "Port to listen on")
	fs.StringVar(&c.Host, "host", c.Host, "Host to bind to")
	fs.StringVar(&c.TokenPath, "token-path", c.TokenPath, "Path to Antigravity OAuth token file")
	fs.StringVar(&c.Project, "project", c.Project, "cloudaicompanionProject for the upstream envelope")
	fs.StringVar(&c.UpstreamURL, "upstream-url", c.UpstreamURL, "Cloud Code Assist base URL")
	fs.StringVar(&c.DefaultModel, "default-model", c.DefaultModel, "Default model fallback")
	fs.IntVar(&c.MaxConcurrency, "max-concurrency", c.MaxConcurrency, "Max concurrent requests")
	fs.BoolVar(&c.Verbose, "verbose", c.Verbose, "Enable verbose logging")
}

// defaultTokenPath returns the Antigravity CLI OAuth token location,
// preferring the isolated gateway HOME when it exists.
func defaultTokenPath(home string) string {
	isolated := filepath.Join(home, ".local", "share", "agyapi", "home", ".gemini", "antigravity-cli", "antigravity-oauth-token")
	if _, err := os.Stat(isolated); err == nil {
		return isolated
	}
	return filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token")
}

// Load initializes configuration with defaults, overrides them with environment variables,
// and binds command line flags to flag.CommandLine.
func Load() *Config {
	home, _ := os.UserHomeDir()
	cfg := &Config{
		Port:           7862,
		Host:           "127.0.0.1",
		DefaultModel:   "gemini-3.8-flash-high",
		MaxConcurrency: 4,
		Verbose:        false,

		TokenPath:     defaultTokenPath(home),
		Project:       "aicode-consumers",
		UpstreamURL:   "https://daily-cloudcode-pa.googleapis.com",
		OAuthTokenURL: "https://oauth2.googleapis.com/token",
		// These identify the Antigravity CLI's installed-app OAuth client.
		// Per RFC 8252 §8.5 such client secrets are not confidential and ship
		// inside the CLI binary itself; they are not account credentials.
		// Override with AGY_OAUTH_CLIENT_ID / AGY_OAUTH_CLIENT_SECRET.
		OAuthClientID:     "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com",
		OAuthClientSecret: "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",
	}

	if envPort := os.Getenv("PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil {
			cfg.Port = p
		}
	}
	if envHost := os.Getenv("HOST"); envHost != "" {
		cfg.Host = envHost
	}
	if envModel := os.Getenv("DEFAULT_MODEL"); envModel != "" {
		cfg.DefaultModel = envModel
	}
	if envConc := os.Getenv("MAX_CONCURRENCY"); envConc != "" {
		if c, err := strconv.Atoi(envConc); err == nil {
			cfg.MaxConcurrency = c
		}
	}
	if os.Getenv("VERBOSE") == "true" || os.Getenv("VERBOSE") == "1" {
		cfg.Verbose = true
	}

	if v := os.Getenv("AGY_TOKEN_PATH"); v != "" {
		cfg.TokenPath = v
	}
	if v := os.Getenv("AGY_PROJECT"); v != "" {
		cfg.Project = v
	}
	if v := os.Getenv("AGY_UPSTREAM_URL"); v != "" {
		cfg.UpstreamURL = v
	}
	if v := os.Getenv("AGY_OAUTH_TOKEN_URL"); v != "" {
		cfg.OAuthTokenURL = v
	}
	if v := os.Getenv("AGY_OAUTH_CLIENT_ID"); v != "" {
		cfg.OAuthClientID = v
	}
	if v := os.Getenv("AGY_OAUTH_CLIENT_SECRET"); v != "" {
		cfg.OAuthClientSecret = v
	}

	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 4
	}

	if flag.Lookup("port") == nil {
		cfg.BindFlags(flag.CommandLine)
	}

	return cfg
}
