// Package hduhelp is the entry point for the hduhelp-neo Go SDK, modeled on the
// Feishu (larksuite/oapi-sdk-go) ergonomics.
//
// Construct a client with app credentials, then call namespaced services with
// fluent request builders:
//
//	client := hduhelp.NewClient(appID, appSecret)
//	req := academic.NewScheduleReqBuilder().SchoolYear("2025-2026").Semester(1).Build()
//	resp, err := client.Academic.Schedule(ctx, req)
//	if err != nil { return err }
//	if !resp.Success() { log.Println(resp.Code, resp.Msg, resp.RequestID()); return }
//	use(resp.Data)
//
// The client fetches, caches, and auto-refreshes the tenant_access_token and
// injects it as `Authorization: Bearer <token>`. Override auth per call with the
// WithUserAccessToken / WithTenantAccessToken / WithPAT request options.
package hduhelp

import "github.com/hduhelp/hduhelp-neo-sdk-go/core"

// NewClient builds a client from app credentials. With no options it manages the
// tenant_access_token automatically; options tune the base URL, HTTP client,
// timeout, token caching, a default PAT, and log level.
func NewClient(appID, appSecret string, opts ...ClientOption) *Client {
	c := &Client{config: core.NewConfig(appID, appSecret, opts...)}
	attachServices(c)
	return c
}

// Config exposes the underlying client configuration.
func (c *Client) Config() *core.Config { return c.config }

// UserAuth returns the OAuth2 + PKCE helper bound to this client's credentials.
func (c *Client) UserAuth() *core.UserAuth { return c.config.UserAuth() }

// Option and token types re-exported so callers depend only on this package.
type (
	ClientOption  = core.ClientOption
	RequestOption = core.RequestOption
	LogLevel      = core.LogLevel

	PKCE            = core.PKCE
	UserAuth        = core.UserAuth
	UserToken       = core.UserToken
	UserTokenSource = core.UserTokenSource
	AuthorizeParams = core.AuthorizeParams
)

// Log levels.
const (
	LogLevelError = core.LogLevelError
	LogLevelWarn  = core.LogLevelWarn
	LogLevelInfo  = core.LogLevelInfo
	LogLevelDebug = core.LogLevelDebug
)

// DefaultBaseURL is the gateway used when WithBaseURL is not supplied.
const DefaultBaseURL = core.DefaultBaseURL

// Client construction options.
var (
	WithBaseURL          = core.WithBaseURL
	WithHTTPClient       = core.WithHTTPClient
	WithReqTimeout       = core.WithReqTimeout
	WithEnableTokenCache = core.WithEnableTokenCache
	WithLogLevel         = core.WithLogLevel
)

// Auth options. WithPAT works both at construction (client default) and as a
// trailing per-request option; WithUserAccessToken and WithTenantAccessToken
// override auth for a single call.
var (
	WithPAT               = core.WithPAT
	WithUserAccessToken   = core.WithUserAccessToken
	WithTenantAccessToken = core.WithTenantAccessToken
)

// PKCE / user-flow helpers.
var (
	GeneratePKCE       = core.GeneratePKCE
	S256Challenge      = core.S256Challenge
	NewUserTokenSource = core.NewUserTokenSource
)
