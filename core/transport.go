package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// APIReq is the transport-level description of a single call, produced by the
// generated service methods from a request builder.
type APIReq struct {
	HTTPMethod   string
	PathTemplate string            // e.g. "/hduhelp-neo/identity/login/bind/{state}"
	PathParams   map[string]string // template placeholder -> value
	QueryParams  map[string]string
	Body         any // nil for bodyless requests
}

const maxAPIResponseBytes = 8 << 20

// Do executes req, authenticates it, and decodes the JSON body into result
// (which must embed core.APIResp so the RawResponse can be attached). A non-nil
// error is returned for transport failures and non-2xx statuses; business-level
// failures are reported via result.Success()/Code/Msg with a nil error.
func (c *Config) Do(ctx context.Context, req *APIReq, result ResponseSetter, opts ...RequestOption) error {
	ro := &RequestOptions{}
	for _, o := range opts {
		o.applyRequest(ro)
	}

	endpoint, err := c.buildURL(req)
	if err != nil {
		return err
	}

	var bodyReader io.Reader
	if req.Body != nil {
		raw, err := json.Marshal(req.Body)
		if err != nil {
			return fmt.Errorf("hduhelp: marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(raw)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.HTTPMethod, endpoint, bodyReader)
	if err != nil {
		return fmt.Errorf("hduhelp: build request: %w", err)
	}
	if req.Body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	httpReq.Header.Set("Accept", "application/json")

	token, err := c.resolveToken(ctx, ro)
	if err != nil {
		return err
	}
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}

	httpResp, err := c.httpClient().Do(httpReq)
	if err != nil {
		return fmt.Errorf("hduhelp: %s %s: %w", req.HTTPMethod, req.PathTemplate, err)
	}
	defer httpResp.Body.Close()
	rawBody, err := io.ReadAll(io.LimitReader(httpResp.Body, maxAPIResponseBytes))
	if err != nil {
		return fmt.Errorf("hduhelp: read response: %w", err)
	}

	raw := &RawResponse{
		StatusCode: httpResp.StatusCode,
		Header:     httpResp.Header,
		RawBody:    rawBody,
	}

	if httpResp.StatusCode/100 != 2 {
		result.SetRawResponse(raw)
		return fmt.Errorf("hduhelp: %s %s returned HTTP %d: %s",
			req.HTTPMethod, req.PathTemplate, httpResp.StatusCode, truncate(string(rawBody)))
	}
	if len(rawBody) > 0 {
		if err := json.Unmarshal(rawBody, result); err != nil {
			result.SetRawResponse(raw)
			return fmt.Errorf("hduhelp: decode response: %w", err)
		}
	}
	result.SetRawResponse(raw)
	return nil
}

func (c *Config) buildURL(req *APIReq) (string, error) {
	path := req.PathTemplate
	for name, value := range req.PathParams {
		placeholder := "{" + name + "}"
		if !strings.Contains(path, placeholder) {
			return "", fmt.Errorf("hduhelp: path %q has no placeholder %q", req.PathTemplate, placeholder)
		}
		path = strings.ReplaceAll(path, placeholder, url.PathEscape(value))
	}
	endpoint := c.baseURL() + path
	if len(req.QueryParams) > 0 {
		q := url.Values{}
		for k, v := range req.QueryParams {
			q.Set(k, v)
		}
		endpoint += "?" + q.Encode()
	}
	return endpoint, nil
}
