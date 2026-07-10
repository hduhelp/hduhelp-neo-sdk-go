package core

import (
	"net/http"
	"time"
)

// DefaultBaseURL is the hduhelp-neo gateway used when none is configured.
const DefaultBaseURL = "https://open.hduhelp.com"

// DefaultReqTimeout bounds a single API/token HTTP call when the caller does not
// supply their own *http.Client. It prevents a hung endpoint from blocking a
// refresh (and therefore every queued caller) indefinitely.
const DefaultReqTimeout = 30 * time.Second

// LogLevel controls the client's internal logging verbosity.
type LogLevel int

const (
	LogLevelError LogLevel = iota
	LogLevelWarn
	LogLevelInfo
	LogLevelDebug
)

// ClientOption configures a Client at construction time.
type ClientOption interface{ applyClient(*Config) }

// RequestOption overrides settings for a single API call (currently the auth
// token). Pass these as trailing arguments to any service method.
type RequestOption interface{ applyRequest(*RequestOptions) }

// RequestOptions holds the per-request overrides after options are applied.
type RequestOptions struct {
	UserAccessToken   string
	TenantAccessToken string
	PAT               string
}

type clientOptionFunc func(*Config)

func (f clientOptionFunc) applyClient(c *Config) { f(c) }

// WithBaseURL overrides the gateway base URL.
func WithBaseURL(url string) ClientOption {
	return clientOptionFunc(func(c *Config) { c.BaseURL = url })
}

// WithHTTPClient sets the HTTP client used for both auth and API calls.
func WithHTTPClient(hc *http.Client) ClientOption {
	return clientOptionFunc(func(c *Config) { c.HTTPClient = hc })
}

// WithReqTimeout sets the per-request timeout applied when the client builds its
// own HTTP client. It is ignored if WithHTTPClient supplies a client.
func WithReqTimeout(d time.Duration) ClientOption {
	return clientOptionFunc(func(c *Config) { c.ReqTimeout = d })
}

// WithEnableTokenCache toggles automatic tenant_access_token management. It is
// on by default; disable it only when every call supplies its own token option.
func WithEnableTokenCache(enable bool) ClientOption {
	return clientOptionFunc(func(c *Config) { c.EnableTokenCache = enable })
}

// WithLogLevel sets the client log verbosity.
func WithLogLevel(l LogLevel) ClientOption {
	return clientOptionFunc(func(c *Config) { c.LogLevel = l })
}

type requestOptionFunc func(*RequestOptions)

func (f requestOptionFunc) applyRequest(r *RequestOptions) { f(r) }

// WithUserAccessToken authenticates a single call with a user_access_token,
// overriding the client default for that call.
func WithUserAccessToken(token string) RequestOption {
	return requestOptionFunc(func(r *RequestOptions) { r.UserAccessToken = token })
}

// WithTenantAccessToken authenticates a single call with an explicit
// tenant_access_token, overriding the cached one for that call.
func WithTenantAccessToken(token string) RequestOption {
	return requestOptionFunc(func(r *RequestOptions) { r.TenantAccessToken = token })
}

// patOption is usable both as a ClientOption (client-wide default PAT) and as a
// RequestOption (per-call PAT), so a single WithPAT serves both positions.
type patOption string

func (p patOption) applyClient(c *Config)          { c.PAT = string(p) }
func (p patOption) applyRequest(r *RequestOptions) { r.PAT = string(p) }

// WithPAT uses a personal access token (`hduhelp_pat_...`). As a client option
// it becomes the default for every call; as a trailing request option it
// overrides auth for a single call. Injected as `Authorization: Bearer <pat>`.
func WithPAT(token string) patOption { return patOption(token) }
