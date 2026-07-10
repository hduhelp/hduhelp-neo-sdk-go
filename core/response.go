package core

import "net/http"

// RawResponse carries the transport-level result of an API call, independent of
// the typed body. Generated response types embed it (via APIResp) so callers can
// reach the status code, headers, request id, and raw bytes.
type RawResponse struct {
	StatusCode int
	Header     http.Header
	RawBody    []byte
}

// requestIDHeaders lists the response headers that may carry the server's
// request id, in priority order.
var requestIDHeaders = []string{"X-Request-Id", "X-Request-ID", "X-Tt-Logid", "Request-Id"}

// RequestID returns the server-assigned request id, or "" if none is present.
// Include it when reporting a failed call so the API team can trace it.
func (r *RawResponse) RequestID() string {
	if r == nil || r.Header == nil {
		return ""
	}
	for _, h := range requestIDHeaders {
		if v := r.Header.Get(h); v != "" {
			return v
		}
	}
	return ""
}

// CodeMsg is the `{code, msg}` pair every API response carries. Generated
// response types embed it, exposing resp.Code, resp.Msg, and resp.Success().
type CodeMsg struct {
	Code int64  `json:"code"`
	Msg  string `json:"msg"`
}

// Success reports whether the business code indicates success (code == 0).
func (c CodeMsg) Success() bool { return c.Code == 0 }

// APIResp is embedded by every generated response type to carry the raw
// transport result. The transport sets it after decoding the typed body.
type APIResp struct {
	*RawResponse `json:"-"`
}

// SetRawResponse implements ResponseSetter.
func (a *APIResp) SetRawResponse(r *RawResponse) { a.RawResponse = r }

// ResponseSetter is implemented by every generated response type (through the
// embedded APIResp) so the transport can attach the RawResponse generically.
type ResponseSetter interface {
	SetRawResponse(*RawResponse)
}
