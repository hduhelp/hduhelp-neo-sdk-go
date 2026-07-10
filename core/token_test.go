package core

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func tokenServer(hits *int64, expire int64, delay time.Duration) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	srv := tokenServer(&hits, 7200, 0)
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
	srv := tokenServer(&hits, 7200, 0)
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
	srv := tokenServer(&hits, 7200, 30*time.Millisecond)
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

func TestTokenContextHonoredWhileRefreshing(t *testing.T) {
	// A slow endpoint: a caller with an already-cancelled context must not block
	// on the in-flight refresh.
	var hits int64
	srv := tokenServer(&hits, 7200, 200*time.Millisecond)
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
