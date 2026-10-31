package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// refreshSkew refreshes slightly before the real expiry so a request never
// races the clock.
const refreshSkew = 2 * time.Minute

// Store owns the Antigravity OAuth token file. It never touches the macOS
// Keychain: the file is the single source of truth, which keeps the gateway
// free of the "A keychain cannot be found to store ..." dialog.
//
// The file is re-read on every call rather than cached in memory, because more
// than one process may legitimately own the same credential (the CLI during
// re-authentication, a second gateway instance). Cached state would either
// serve a revoked token or clobber a peer's freshly rotated refresh token.
type Store struct {
	Path         string
	TokenURL     string
	ClientID     string
	ClientSecret string
	HTTPClient   *http.Client
	// Now is injectable for tests.
	Now func() time.Time

	mu sync.Mutex
}

// NewStore returns a Store bound to a token file path.
func NewStore(path string) *Store {
	return &Store{
		Path:       path,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Now:        time.Now,
	}
}

// AccessToken returns a usable access token, refreshing and persisting when
// the stored one is missing or close to expiry. Concurrent callers serialize,
// so a burst of requests triggers exactly one refresh.
func (s *Store) AccessToken(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.read()
	if err != nil {
		return "", err
	}
	if tok, ok := usableToken(doc, s.now()); ok {
		return tok, nil
	}
	return s.refresh(ctx, doc)
}

// usableToken reports a token that is still valid beyond the refresh skew.
func usableToken(doc map[string]any, now time.Time) (string, bool) {
	sec := tokenSection(doc)
	if sec == nil {
		return "", false
	}
	tok, _ := sec["access_token"].(string)
	if tok == "" {
		return "", false
	}
	exp, ok := parseExpiry(sec)
	if !ok {
		return "", false
	}
	if now.Before(exp.Add(-refreshSkew)) {
		return tok, true
	}
	return "", false
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Store) httpClient() *http.Client {
	if s.HTTPClient != nil {
		return s.HTTPClient
	}
	return http.DefaultClient
}

func (s *Store) read() (map[string]any, error) {
	raw, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, fmt.Errorf("read token file %s: %w", s.Path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse token file %s: %w", s.Path, err)
	}
	if tokenSection(doc) == nil {
		return nil, fmt.Errorf("token file %s has no \"token\" object", s.Path)
	}
	return doc, nil
}

func tokenSection(doc map[string]any) map[string]any {
	if doc == nil {
		return nil
	}
	sec, _ := doc["token"].(map[string]any)
	return sec
}

func parseExpiry(sec map[string]any) (time.Time, bool) {
	raw, _ := sec["expiry"].(string)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// refresh exchanges the refresh_token for a new access token. Google rotates
// the refresh token, so the new value is persisted immediately — unless a peer
// process already rotated first, in which case the peer's state wins.
func (s *Store) refresh(ctx context.Context, doc map[string]any) (string, error) {
	sec := tokenSection(doc)
	refreshToken, _ := sec["refresh_token"].(string)
	if refreshToken == "" {
		return "", fmt.Errorf("access token expired and no refresh_token present in %s; re-authenticate with agy-login", s.Path)
	}
	if s.TokenURL == "" || s.ClientID == "" || s.ClientSecret == "" {
		return "", fmt.Errorf("oauth refresh is not configured (token url / client id / client secret)")
	}

	form := url.Values{}
	form.Set("client_id", s.ClientID)
	form.Set("client_secret", s.ClientSecret)
	form.Set("refresh_token", refreshToken)
	form.Set("grant_type", "refresh_token")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient().Do(req)
	if err != nil {
		return s.adoptPeer(fmt.Errorf("refresh request failed: %w", err))
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusOK {
		return s.adoptPeer(fmt.Errorf("refresh rejected (%d): %s", resp.StatusCode, strings.TrimSpace(string(body))))
	}

	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("decode refresh response: %w", err)
	}
	if out.AccessToken == "" {
		return s.adoptPeer(fmt.Errorf("refresh response contained no access_token"))
	}

	// Re-read: if the on-disk refresh_token is no longer the one we sent, a peer
	// rotated while we were in flight. Adopt its coherent state instead of
	// overwriting it with a token pair whose refresh half is now dead.
	if latest, readErr := s.read(); readErr == nil {
		latestSec := tokenSection(latest)
		if latestRT, _ := latestSec["refresh_token"].(string); latestRT != "" && latestRT != refreshToken {
			if tok, ok := usableToken(latest, s.now()); ok {
				return tok, nil
			}
			doc = latest
			sec = latestSec
		}
	}

	expiresIn := out.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3599
	}
	expiry := s.now().Add(time.Duration(expiresIn) * time.Second)

	sec["access_token"] = out.AccessToken
	sec["token_type"] = "Bearer"
	sec["expires_in"] = expiresIn
	sec["expiry"] = expiry.Format(time.RFC3339Nano)
	if out.RefreshToken != "" {
		sec["refresh_token"] = out.RefreshToken
	}
	doc["token"] = sec

	if err := s.persist(doc); err != nil {
		// The token is still usable in memory; a persistence failure must not
		// break the request, but it must be visible.
		fmt.Fprintf(os.Stderr, "agyapi: warning: could not persist refreshed token: %v\n", err)
	}

	return out.AccessToken, nil
}

// adoptPeer re-reads the token file after a failed refresh. If a peer managed
// to write a usable token, use it instead of surfacing the failure.
func (s *Store) adoptPeer(cause error) (string, error) {
	if latest, err := s.read(); err == nil {
		if tok, ok := usableToken(latest, s.now()); ok {
			return tok, nil
		}
	}
	return "", cause
}

// persist writes the token document atomically with 0600 permissions.
func (s *Store) persist(doc map[string]any) error {
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')

	dir := filepath.Dir(s.Path)
	tmp, err := os.CreateTemp(dir, ".agy-token-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, s.Path); err != nil {
		cleanup()
		return err
	}
	return nil
}
