package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	pathTenantToken = "/hduhelp-neo/open-apis/auth/v3/tenant_access_token/internal"
	pathAppToken    = "/hduhelp-neo/open-apis/auth/v3/app_access_token/internal"

	// earlyRefresh is how long before the server-reported expiry a cached token
	// is proactively refreshed, absorbing clock skew and request latency.
	earlyRefresh = 60 * time.Second

	maxTokenResponseBytes = 1 << 20
)

// envelope is the `{code, msg, data}` wrapper the API returns.
type envelope struct {
	Code *int64          `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

type tenantTokenData struct {
	TenantAccessToken string `json:"tenantAccessToken"`
	Expire            int64  `json:"expire"`
	TenantKey         string `json:"tenantKey"`
}

// lifetimeOf is the token's usable lifetime. `expireSeconds` is the
// server-reported remaining lifetime; a non-positive value falls back to two
// hours so a malformed response still yields a bounded cache window.
func lifetimeOf(expireSeconds int64) time.Duration {
	lifetime := time.Duration(expireSeconds) * time.Second
	if lifetime <= 0 {
		lifetime = 2 * time.Hour
	}
	return lifetime
}

// earlyLead is the lead time before expiry at which a token is proactively
// refreshed, clamped so it never exceeds half the lifetime.
func earlyLead(lifetime time.Duration) time.Duration {
	early := earlyRefresh
	if early > lifetime/2 {
		early = lifetime / 2
	}
	return early
}

// window returns the soft refresh instant (expiry minus the early-refresh lead,
// when a proactive refresh begins) and the hard expiry instant (after which the
// token is no longer accepted). Between the two, a failed refresh may still
// serve the cached token.
func window(now time.Time, expireSeconds int64) (refresh, expiry time.Time) {
	lifetime := lifetimeOf(expireSeconds)
	expiry = now.Add(lifetime)
	refresh = now.Add(lifetime - earlyLead(lifetime))
	return refresh, expiry
}

func truncate(s string) string {
	const max = 512
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// postEnvelope POSTs payload as JSON and returns the decoded `data`. It surfaces
// the server's `msg` (or raw body) on non-2xx responses. The request body (which
// carries app_secret) never appears in an error, so secrets do not leak.
func postEnvelope(ctx context.Context, hc *http.Client, url string, payload any) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("hduhelp: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("hduhelp: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hduhelp: request to %s: %w", url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("hduhelp: read response: %w", err)
	}

	var env envelope
	decodeErr := json.Unmarshal(raw, &env)
	if resp.StatusCode/100 != 2 {
		detail := env.Msg
		if detail == "" {
			detail = string(raw)
		}
		return nil, fmt.Errorf("hduhelp: %s returned HTTP %d: %s", url, resp.StatusCode, truncate(detail))
	}
	if decodeErr != nil {
		return nil, fmt.Errorf("hduhelp: decode response envelope: %w", decodeErr)
	}
	if env.Code != nil && *env.Code != 0 {
		return nil, fmt.Errorf("hduhelp: %s returned code %d: %s", url, *env.Code, truncate(env.Msg))
	}
	return env.Data, nil
}

// tenantTokenCache fetches, caches, and auto-refreshes the tenant_access_token.
// The shared refresh runs in its own goroutine on a detached context; waiters
// block on a channel and can abandon the wait via their own ctx. A caller's
// deadline is therefore always honored, and one caller's cancellation never
// aborts a refresh other callers depend on.
type tenantTokenCache struct {
	cfg *Config

	mu       sync.Mutex
	token    string
	refresh  time.Time // soft: begin a proactive refresh after this
	expiry   time.Time // hard: token no longer accepted after this
	inflight chan struct{}
	lastErr  error
	nowFn    func() time.Time
}

func newTenantTokenCache(cfg *Config) *tenantTokenCache {
	return &tenantTokenCache{cfg: cfg}
}

func (c *tenantTokenCache) now() time.Time {
	if c.nowFn != nil {
		return c.nowFn()
	}
	return time.Now()
}

func (c *tenantTokenCache) Token(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.token != "" && c.now().Before(c.refresh) {
		token := c.token
		c.mu.Unlock()
		return token, nil
	}
	if c.inflight == nil {
		done := make(chan struct{})
		c.inflight = done
		go c.doRefresh(done)
	}
	wait := c.inflight
	c.mu.Unlock()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-wait:
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	// Serve a still-valid token even if the just-finished refresh failed: within
	// the early-refresh grace the cached token is still accepted by the server,
	// so a transient token-endpoint outage does not break every call.
	if c.token != "" && c.now().Before(c.expiry) {
		return c.token, nil
	}
	if c.lastErr != nil {
		return "", c.lastErr
	}
	if c.token != "" {
		return c.token, nil
	}
	return "", fmt.Errorf("hduhelp: tenant token unavailable after refresh")
}

func (c *tenantTokenCache) doRefresh(done chan struct{}) {
	// Bound the detached refresh so a hung endpoint (even behind a caller-
	// supplied http.Client with no timeout) cannot wedge the cache forever.
	ctx, cancel := context.WithTimeout(context.Background(), c.cfg.refreshTimeout())
	defer cancel()
	token, expire, err := c.fetch(ctx)
	c.mu.Lock()
	if err != nil {
		c.lastErr = err
	} else {
		c.lastErr = nil
		c.token = token
		c.refresh, c.expiry = window(c.now(), expire)
	}
	c.inflight = nil
	close(done)
	c.mu.Unlock()
}

func (c *tenantTokenCache) fetch(ctx context.Context) (string, int64, error) {
	data, err := postEnvelope(ctx, c.cfg.httpClient(), c.cfg.baseURL()+pathTenantToken, map[string]string{
		"app_id":     c.cfg.AppID,
		"app_secret": c.cfg.AppSecret,
	})
	if err != nil {
		return "", 0, err
	}
	var d tenantTokenData
	if err := json.Unmarshal(data, &d); err != nil {
		return "", 0, fmt.Errorf("hduhelp: decode tenant token data: %w", err)
	}
	if d.TenantAccessToken == "" {
		return "", 0, fmt.Errorf("hduhelp: tenant token endpoint returned no token")
	}
	return d.TenantAccessToken, d.Expire, nil
}
