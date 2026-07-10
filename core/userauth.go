package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"time"
)

const (
	pathAuthorize   = "/hduhelp-neo/open-apis/authen/v1/authorize"
	pathAccessToken = "/hduhelp-neo/open-apis/authen/v1/access_token"
	pathRefreshUser = "/hduhelp-neo/open-apis/authen/v1/refresh_access_token"
)

// AuthorizeParams configures the OAuth2 authorize URL.
type AuthorizeParams struct {
	RedirectURI string
	// Scope is a comma-separated subset of the app's granted scopes.
	Scope string
	State string
	// PKCE carries the S256 challenge placed in the URL. Generate it with
	// GeneratePKCE and keep PKCE.Verifier for the token exchange.
	PKCE PKCE
}

// UserToken is the result of an authorization-code exchange or refresh.
type UserToken struct {
	AccessToken  string
	RefreshToken string
	// ExpiresIn is the access-token lifetime in seconds.
	ExpiresIn int64
	Scope     string
	TenantKey string
	TokenType string
	UserID    string
}

type userTokenData struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int64  `json:"expiresIn"`
	Scope        string `json:"scope"`
	TenantKey    string `json:"tenantKey"`
	TokenType    string `json:"tokenType"`
	UserID       string `json:"userId"`
}

// UserAuth drives the OAuth2 + PKCE user flow against the current
// /hduhelp-neo/open-apis/authen/v1/* surface.
type UserAuth struct {
	cfg *Config
}

// UserAuth returns a helper bound to this client's credentials and base URL.
func (c *Config) UserAuth() *UserAuth { return &UserAuth{cfg: c} }

// AuthorizeURL builds the authorize URL carrying the PKCE S256 challenge.
func (u *UserAuth) AuthorizeURL(p AuthorizeParams) (string, error) {
	if u.cfg.AppID == "" {
		return "", fmt.Errorf("hduhelp: AppID is required")
	}
	if p.RedirectURI == "" {
		return "", fmt.Errorf("hduhelp: RedirectURI is required")
	}
	q := url.Values{}
	q.Set("app_id", u.cfg.AppID)
	q.Set("redirect_uri", p.RedirectURI)
	q.Set("response_type", "code")
	if p.PKCE.Challenge != "" {
		method := p.PKCE.Method
		if method == "" {
			method = "S256"
		}
		q.Set("code_challenge", p.PKCE.Challenge)
		q.Set("code_challenge_method", method)
	}
	if p.Scope != "" {
		q.Set("scope", p.Scope)
	}
	if p.State != "" {
		q.Set("state", p.State)
	}
	return u.cfg.baseURL() + pathAuthorize + "?" + q.Encode(), nil
}

// ExchangeCode swaps an authorization code plus its PKCE verifier for a user
// token (RFC 7636 §4.5: the server recomputes the challenge from the verifier).
func (u *UserAuth) ExchangeCode(ctx context.Context, code, codeVerifier string) (UserToken, error) {
	return u.post(ctx, pathAccessToken, map[string]string{
		"grant_type":    "authorization_code",
		"app_id":        u.cfg.AppID,
		"app_secret":    u.cfg.AppSecret,
		"code":          code,
		"code_verifier": codeVerifier,
	})
}

// Refresh rotates a user token using its refresh token.
func (u *UserAuth) Refresh(ctx context.Context, refreshToken string) (UserToken, error) {
	return u.post(ctx, pathRefreshUser, map[string]string{
		"grant_type":    "refresh_token",
		"app_id":        u.cfg.AppID,
		"app_secret":    u.cfg.AppSecret,
		"refresh_token": refreshToken,
	})
}

func (u *UserAuth) post(ctx context.Context, path string, payload map[string]string) (UserToken, error) {
	data, err := postEnvelope(ctx, u.cfg.httpClient(), u.cfg.baseURL()+path, payload)
	if err != nil {
		return UserToken{}, err
	}
	var d userTokenData
	if err := json.Unmarshal(data, &d); err != nil {
		return UserToken{}, fmt.Errorf("hduhelp: decode user token data: %w", err)
	}
	if d.AccessToken == "" {
		return UserToken{}, fmt.Errorf("hduhelp: %s returned no accessToken", path)
	}
	return UserToken(d), nil
}

// UserTokenSource is a token source backed by a user token that auto-refreshes
// via its refresh token before expiry. Seed it with the result of ExchangeCode
// and pass its Token() output as WithUserAccessToken on calls, or use
// NewUserTokenSource().Token(ctx) directly. It is safe for concurrent use.
type UserTokenSource struct {
	auth *UserAuth

	mu           sync.Mutex
	accessToken  string
	refreshToken string
	refresh      time.Time // soft: begin a proactive refresh after this
	expiry       time.Time // hard: token no longer accepted after this
	inflight     chan struct{}
	lastErr      error
	nowFn        func() time.Time
}

// NewUserTokenSource builds an auto-refreshing source from a seed token. When
// the seed's ExpiresIn is unknown (<= 0) but a refresh token is present, the
// first Token call refreshes immediately rather than trusting a possibly-stale
// access token for the fallback lifetime.
func NewUserTokenSource(auth *UserAuth, seed UserToken) *UserTokenSource {
	s := &UserTokenSource{
		auth:         auth,
		accessToken:  seed.AccessToken,
		refreshToken: seed.RefreshToken,
	}
	if seed.ExpiresIn <= 0 && seed.RefreshToken != "" {
		// Zero instants are already in the past: fast path skipped, refresh forced.
		return s
	}
	s.refresh, s.expiry = window(s.now(), seed.ExpiresIn)
	return s
}

func (s *UserTokenSource) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

// RefreshToken returns the current (possibly rotated) refresh token so callers
// can persist it across restarts.
func (s *UserTokenSource) RefreshToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refreshToken
}

// Token returns a valid user access token, refreshing before expiry. The shared
// refresh runs on a detached context; waiters honor their own ctx.
func (s *UserTokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	if s.accessToken != "" && s.now().Before(s.refresh) {
		token := s.accessToken
		s.mu.Unlock()
		return token, nil
	}
	if s.refreshToken == "" {
		s.mu.Unlock()
		return "", fmt.Errorf("hduhelp: user token expired and no refresh token is available")
	}
	if s.inflight == nil {
		done := make(chan struct{})
		s.inflight = done
		go s.doRefresh(done)
	}
	wait := s.inflight
	s.mu.Unlock()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-wait:
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Serve a still-valid access token even if the just-finished refresh failed:
	// within the early-refresh grace it is still accepted by the server.
	if s.accessToken != "" && s.now().Before(s.expiry) {
		return s.accessToken, nil
	}
	if s.lastErr != nil {
		return "", s.lastErr
	}
	if s.accessToken != "" {
		return s.accessToken, nil
	}
	return "", fmt.Errorf("hduhelp: user token unavailable after refresh")
}

func (s *UserTokenSource) doRefresh(done chan struct{}) {
	s.mu.Lock()
	rt := s.refreshToken
	s.mu.Unlock()

	// Bound the detached refresh so a hung endpoint cannot wedge the source.
	ctx, cancel := context.WithTimeout(context.Background(), s.auth.cfg.refreshTimeout())
	defer cancel()
	tok, err := s.auth.Refresh(ctx, rt)

	s.mu.Lock()
	if err != nil {
		s.lastErr = err
	} else {
		s.lastErr = nil
		s.accessToken = tok.AccessToken
		if tok.RefreshToken != "" {
			// The server rotates refresh tokens single-use; keep the newest.
			s.refreshToken = tok.RefreshToken
		}
		s.refresh, s.expiry = window(s.now(), tok.ExpiresIn)
	}
	s.inflight = nil
	close(done)
	s.mu.Unlock()
}
