package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func writeTokenFile(t *testing.T, dir string, f map[string]any) string {
	t.Helper()
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "antigravity-oauth-token")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func tokenDoc(access, refresh, expiry string) map[string]any {
	return map[string]any{
		"token": map[string]any{
			"access_token":  access,
			"token_type":    "Bearer",
			"refresh_token": refresh,
			"expiry":        expiry,
			"expires_in":    3599,
		},
		"auth_method":    "consumer",
		"id_token_email": "user@example.com",
	}
}

func TestStoreReturnsCachedTokenWhenStillValid(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	p := writeTokenFile(t, dir, tokenDoc("cached-token", "rt-1", now.Add(time.Hour).Format(time.RFC3339)))

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := NewStore(p)
	s.TokenURL = srv.URL
	s.Now = func() time.Time { return now }

	got, err := s.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if got != "cached-token" {
		t.Fatalf("token = %q", got)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Fatalf("refresh endpoint called %d times, want 0", n)
	}
}

func TestStoreRefreshesExpiredTokenAndPersistsRotation(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	p := writeTokenFile(t, dir, tokenDoc("stale-token", "rt-old", now.Add(-time.Hour).Format(time.RFC3339)))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if r.Form.Get("grant_type") != "refresh_token" {
			t.Errorf("grant_type = %q", r.Form.Get("grant_type"))
		}
		if r.Form.Get("refresh_token") != "rt-old" {
			t.Errorf("refresh_token = %q", r.Form.Get("refresh_token"))
		}
		if r.Form.Get("client_id") == "" || r.Form.Get("client_secret") == "" {
			t.Error("client credentials not sent")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "fresh-token",
			"refresh_token": "rt-new",
			"expires_in":    3599,
			"token_type":    "Bearer",
		})
	}))
	defer srv.Close()

	s := NewStore(p)
	s.TokenURL = srv.URL
	s.ClientID = "cid"
	s.ClientSecret = "csec"
	s.Now = func() time.Time { return now }

	got, err := s.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if got != "fresh-token" {
		t.Fatalf("token = %q, want fresh-token", got)
	}

	var persisted map[string]any
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	tok := persisted["token"].(map[string]any)
	if tok["access_token"] != "fresh-token" {
		t.Fatalf("persisted access_token = %v", tok["access_token"])
	}
	if tok["refresh_token"] != "rt-new" {
		t.Fatalf("rotated refresh_token not persisted: %v", tok["refresh_token"])
	}
	if persisted["id_token_email"] != "user@example.com" {
		t.Fatalf("unrelated fields lost: %v", persisted)
	}

	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %v, want 0600", perm)
	}

	// Second call must reuse the freshly cached token.
	atomic.StoreInt32(new(int32), 0)
	again, err := s.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken #2: %v", err)
	}
	if again != "fresh-token" {
		t.Fatalf("second token = %q", again)
	}
}

func TestStoreRefreshFailureLeavesFileIntact(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	p := writeTokenFile(t, dir, tokenDoc("stale", "rt-old", now.Add(-time.Hour).Format(time.RFC3339)))
	before, _ := os.ReadFile(p)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()

	s := NewStore(p)
	s.TokenURL = srv.URL
	s.ClientID = "cid"
	s.ClientSecret = "csec"
	s.Now = func() time.Time { return now }

	if _, err := s.AccessToken(context.Background()); err == nil {
		t.Fatal("expected error")
	}

	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Fatalf("token file mutated on failure:\nbefore=%s\nafter =%s", before, after)
	}
}

func TestStoreSingleFlightRefresh(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	p := writeTokenFile(t, dir, tokenDoc("stale", "rt-old", now.Add(-time.Hour).Format(time.RFC3339)))

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(60 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fresh", "expires_in": 3599, "token_type": "Bearer",
		})
	}))
	defer srv.Close()

	s := NewStore(p)
	s.TokenURL = srv.URL
	s.ClientID = "cid"
	s.ClientSecret = "csec"
	s.Now = func() time.Time { return now }

	var wg sync.WaitGroup
	tokens := make([]string, 8)
	errs := make([]error, 8)
	for i := range tokens {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tokens[i], errs[i] = s.AccessToken(context.Background())
		}(i)
	}
	wg.Wait()

	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if tokens[i] != "fresh" {
			t.Fatalf("goroutine %d token = %q", i, tokens[i])
		}
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("refresh called %d times, want exactly 1", n)
	}
}

func TestStoreMissingTokenFile(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "nope"))
	s.ClientID, s.ClientSecret = "cid", "csec"
	if _, err := s.AccessToken(context.Background()); err == nil {
		t.Fatal("expected error for missing token file")
	}
}

func TestStoreExpiredWithoutRefreshToken(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	p := writeTokenFile(t, dir, tokenDoc("stale", "", now.Add(-time.Hour).Format(time.RFC3339)))

	s := NewStore(p)
	s.Now = func() time.Time { return now }
	if _, err := s.AccessToken(context.Background()); err == nil {
		t.Fatal("expected error when refresh token is absent")
	}
}

// A peer (e.g. the CLI, or a second gateway) may rotate the token file while we
// hold an in-memory copy. The store must observe the file, not its cache.
func TestStoreAdoptsExternallyUpdatedToken(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	p := writeTokenFile(t, dir, tokenDoc("tok-1", "rt-1", now.Add(time.Hour).Format(time.RFC3339)))

	s := NewStore(p)
	s.Now = func() time.Time { return now }

	if got, err := s.AccessToken(context.Background()); err != nil || got != "tok-1" {
		t.Fatalf("first call = (%q,%v)", got, err)
	}

	// Peer rewrites the file with a newer token.
	writeTokenFile(t, dir, tokenDoc("tok-2", "rt-2", now.Add(2*time.Hour).Format(time.RFC3339)))

	got, err := s.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if got != "tok-2" {
		t.Fatalf("token = %q, want the peer's tok-2 (stale cache?)", got)
	}
}

// If another writer rotates the refresh token mid-flight, our result must not
// clobber theirs.
func TestStoreDoesNotClobberConcurrentRotation(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	p := writeTokenFile(t, dir, tokenDoc("stale", "rt-old", now.Add(-time.Hour).Format(time.RFC3339)))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Peer wins the race and writes its own state before we respond.
		writeTokenFile(t, dir, tokenDoc("peer-token", "rt-peer", now.Add(time.Hour).Format(time.RFC3339)))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "ours-token", "refresh_token": "rt-ours", "expires_in": 3599,
		})
	}))
	defer srv.Close()

	s := NewStore(p)
	s.TokenURL = srv.URL
	s.ClientID, s.ClientSecret = "cid", "csec"
	s.Now = func() time.Time { return now }

	got, err := s.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if got != "peer-token" && got != "ours-token" {
		t.Fatalf("token = %q, want one of peer-token / ours-token", got)
	}

	var persisted map[string]any
	raw, _ := os.ReadFile(p)
	_ = json.Unmarshal(raw, &persisted)
	rt := persisted["token"].(map[string]any)["refresh_token"]
	if rt == "" {
		t.Fatalf("refresh_token missing after concurrent rotation: %v", persisted)
	}
	// Whatever we kept must still be usable: the pair on disk must be coherent.
	tok := persisted["token"].(map[string]any)["access_token"]
	if tok == "ours-token" && rt != "rt-ours" {
		t.Fatalf("mismatched pair persisted: access=%v refresh=%v", tok, rt)
	}
	if tok == "peer-token" && rt != "rt-peer" {
		t.Fatalf("peer state corrupted: access=%v refresh=%v", tok, rt)
	}
}

// A failed refresh must recover if a peer refreshed the file in the meantime.
func TestStoreAdoptsPeerTokenAfterRefreshFailure(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	p := writeTokenFile(t, dir, tokenDoc("stale", "rt-old", now.Add(-time.Hour).Format(time.RFC3339)))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeTokenFile(t, dir, tokenDoc("peer-token", "rt-peer", now.Add(time.Hour).Format(time.RFC3339)))
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()

	s := NewStore(p)
	s.TokenURL = srv.URL
	s.ClientID, s.ClientSecret = "cid", "csec"
	s.Now = func() time.Time { return now }

	got, err := s.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("expected recovery via peer token, got error: %v", err)
	}
	if got != "peer-token" {
		t.Fatalf("token = %q, want peer-token", got)
	}
}
