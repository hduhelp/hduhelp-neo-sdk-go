package core

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Config is the shared client configuration and transport. One Config backs a
// whole Client and all of its services.
type Config struct {
	AppID     string
	AppSecret string

	BaseURL          string
	HTTPClient       *http.Client
	ReqTimeout       time.Duration
	EnableTokenCache bool
	PAT              string
	LogLevel         LogLevel

	tokenCache *tenantTokenCache
	httpOnce   sync.Once
	httpCached *http.Client
}

// NewConfig builds a Config from app credentials and options, applying defaults
// so NewConfig(appID, appSecret) works zero-config.
func NewConfig(appID, appSecret string, opts ...ClientOption) *Config {
	c := &Config{
		AppID:            appID,
		AppSecret:        appSecret,
		BaseURL:          DefaultBaseURL,
		ReqTimeout:       DefaultReqTimeout,
		EnableTokenCache: true,
		LogLevel:         LogLevelError,
	}
	for _, o := range opts {
		o.applyClient(c)
	}
	c.tokenCache = newTenantTokenCache(c)
	return c
}

func (c *Config) baseURL() string {
	u := c.BaseURL
	if u == "" {
		u = DefaultBaseURL
	}
	return strings.TrimRight(u, "/")
}

// httpClient returns the HTTP client, building one with the configured timeout
// on first use when the caller did not supply their own.
func (c *Config) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	c.httpOnce.Do(func() {
		timeout := c.ReqTimeout
		if timeout <= 0 {
			timeout = DefaultReqTimeout
		}
		c.httpCached = &http.Client{Timeout: timeout}
	})
	return c.httpCached
}

// refreshTimeout bounds a detached token refresh, so a hung endpoint cannot
// wedge the token cache even when the caller supplied a timeout-less HTTP client.
func (c *Config) refreshTimeout() time.Duration {
	if c.ReqTimeout > 0 {
		return c.ReqTimeout
	}
	return DefaultReqTimeout
}

// resolveToken picks the bearer token for a call: an explicit per-request token
// wins (user > tenant > pat), otherwise the client default (PAT, else the cached
// tenant_access_token when token caching is enabled and credentials are set).
// An empty token with a nil error means the request is sent unauthenticated.
func (c *Config) resolveToken(ctx context.Context, ro *RequestOptions) (string, error) {
	switch {
	case ro.UserAccessToken != "":
		return ro.UserAccessToken, nil
	case ro.TenantAccessToken != "":
		return ro.TenantAccessToken, nil
	case ro.PAT != "":
		return ro.PAT, nil
	}
	if c.PAT != "" {
		return c.PAT, nil
	}
	if c.EnableTokenCache && c.AppID != "" && c.AppSecret != "" {
		return c.tokenCache.Token(ctx)
	}
	return "", nil
}
