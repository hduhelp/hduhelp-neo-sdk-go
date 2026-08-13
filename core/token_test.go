package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestApplicationTokenPathsMatchNeo(t *testing.T) {
	if pathTenantToken != "/hduhelp-neo/open-apis/auth/tenant-access-token/internal" {
		t.Fatalf("tenant token path = %q", pathTenantToken)
	}
	if pathAppToken != "/hduhelp-neo/open-apis/auth/app-access-token/internal" {
		t.Fatalf("app token path = %q", pathAppToken)
	}
}

func tokenServer(t *testing.T, hits *int64, expire int64, delay time.Duration) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("tenant token method = %s, want POST", r.Method)
			http.Error(w, "wrong method", http.StatusMethodNotAllowed)
			return
		}
		const wantPath = "/hduhelp-neo/open-apis/auth/tenant-access-token/internal"
		if r.URL.Path != wantPath {
			t.Errorf("tenant token path = %q, want %q", r.URL.Path, wantPath)
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("tenant token Content-Type = %q, want application/json", got)
			http.Error(w, "wrong content type", http.StatusUnsupportedMediaType)
			return
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode tenant token request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if payload["app_id"] != "a" || payload["app_secret"] != "s" {
			t.Errorf("tenant token credentials = %#v", payload)
			http.Error(w, "wrong credentials", http.StatusBadRequest)
			return
		}
		if delay > 0 {
			time.Sleep(delay)
		}
		n := atomic.AddInt64(hits, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"code":0,"msg":"ok","data":{"tenantAccessToken":"tat-%d","expire":%d}}`, n, expire)
	}))
}

func TestTenantTokenCachesUntilExpiry(t *testing.T) {
	var hits int64
	srv := tokenServer(t, &hits, 7200, 0)
	defer srv.Close()

	cfg := NewConfig("a", "s", WithBaseURL(srv.URL))
	for i := 0; i < 5; i++ {
		tok, err := cfg.tokenCache.Token(context.Background())
		if err != nil {
			t.Fatalf("Token: %v", err)
		}
		if tok != "tat-1" {
			t.Fatalf("expected cached tat-1, got %q", tok)
		}
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("expected 1 fetch, got %d", got)
	}
}

func TestTenantTokenRefreshesAfterExpiry(t *testing.T) {
	var hits int64
	srv := tokenServer(t, &hits, 7200, 0)
	defer srv.Close()

	cfg := NewConfig("a", "s", WithBaseURL(srv.URL))
	now := time.Now()
	cfg.tokenCache.nowFn = func() time.Time { return now }
	if _, err := cfg.tokenCache.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Hour)
	tok, err := cfg.tokenCache.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok != "tat-2" {
		t.Fatalf("expected refreshed tat-2, got %q", tok)
	}
	if got := atomic.LoadInt64(&hits); got != 2 {
		t.Fatalf("expected 2 fetches, got %d", got)
	}
}

func TestTenantTokenSingleFlight(t *testing.T) {
	var hits int64
	srv := tokenServer(t, &hits, 7200, 30*time.Millisecond)
	defer srv.Close()

	cfg := NewConfig("a", "s", WithBaseURL(srv.URL))
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cfg.tokenCache.Token(context.Background()); err != nil {
				t.Errorf("Token: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("expected single-flight (1 fetch) under concurrency, got %d", got)
	}
}

func TestTenantTokenServedStaleWithinGraceOnRefreshFailure(t *testing.T) {
	var fail atomic.Bool
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		if fail.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// 120s lifetime -> soft refresh at +60s, hard expiry at +120s.
		fmt.Fprint(w, `{"code":0,"data":{"tenantAccessToken":"tat-1","expire":120}}`)
	}))
	defer srv.Close()

	cfg := NewConfig("a", "s", WithBaseURL(srv.URL))
	t0 := time.Now()
	now := t0
	cfg.tokenCache.nowFn = func() time.Time { return now }

	tok, err := cfg.tokenCache.Token(context.Background())
	if err != nil || tok != "tat-1" {
		t.Fatalf("initial Token = %q, %v", tok, err)
	}

	// Past the soft refresh instant but before hard expiry; the refresh now fails.
	fail.Store(true)
	now = t0.Add(70 * time.Second)
	tok, err = cfg.tokenCache.Token(context.Background())
	if err != nil {
		t.Fatalf("within grace, expected stale token served, got error: %v", err)
	}
	if tok != "tat-1" {
		t.Fatalf("within grace, expected tat-1, got %q", tok)
	}

	// Past hard expiry; the failing refresh must now surface as an error.
	now = t0.Add(130 * time.Second)
	if _, err := cfg.tokenCache.Token(context.Background()); err == nil {
		t.Fatal("past expiry with failing endpoint, expected error, got nil")
	}
}

func TestTokenContextHonoredWhileRefreshing(t *testing.T) {
	// A slow endpoint: a caller with an already-cancelled context must not block
	// on the in-flight refresh.
	var hits int64
	srv := tokenServer(t, &hits, 7200, 200*time.Millisecond)
	defer srv.Close()

	cfg := NewConfig("a", "s", WithBaseURL(srv.URL))
	// Kick off a refresh in the background.
	go cfg.tokenCache.Token(context.Background())
	time.Sleep(10 * time.Millisecond) // let the refresh start

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, err := cfg.tokenCache.Token(ctx)
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("cancelled caller blocked %v on in-flight refresh", elapsed)
	}
}
