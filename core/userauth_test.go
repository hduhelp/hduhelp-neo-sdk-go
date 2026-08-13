package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
)

func TestAuthorizeURLMatchesNeoRoute(t *testing.T) {
	cfg := NewConfig("cli_test", "secret", WithBaseURL("https://api.example.com"))
	got, err := cfg.UserAuth().AuthorizeURL(AuthorizeParams{
		RedirectURI: "https://app.example.com/callback",
		Scope:       "identity:staffid:read,academic:schedule:read",
		State:       "state-123",
		PKCE: PKCE{
			Challenge: "challenge-123",
			Method:    "S256",
		},
	})
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}

	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse authorize URL: %v", err)
	}
	const wantPath = "/hduhelp-neo/open-apis/authen/authorize"
	if parsed.Path != wantPath {
		t.Fatalf("authorize path = %q, want %q", parsed.Path, wantPath)
	}
	wantQuery := url.Values{
		"app_id":                {"cli_test"},
		"redirect_uri":          {"https://app.example.com/callback"},
		"response_type":         {"code"},
		"scope":                 {"identity:staffid:read,academic:schedule:read"},
		"state":                 {"state-123"},
		"code_challenge":        {"challenge-123"},
		"code_challenge_method": {"S256"},
	}
	if gotQuery := parsed.Query(); !reflect.DeepEqual(gotQuery, wantQuery) {
		t.Fatalf("authorize query = %#v, want %#v", gotQuery, wantQuery)
	}
}

func TestAuthorizeURLAllowsOmittedRedirectURI(t *testing.T) {
	cfg := NewConfig("cli_test", "secret", WithBaseURL("https://api.example.com"))
	got, err := cfg.UserAuth().AuthorizeURL(AuthorizeParams{})
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse authorize URL: %v", err)
	}
	if _, present := parsed.Query()["redirect_uri"]; present {
		t.Fatalf("redirect_uri should be omitted, got %q", parsed.Query().Get("redirect_uri"))
	}
}

func TestExchangeCodeUsesNeoFormContract(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/hduhelp-neo/open-apis/authen/access-token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
			http.Error(w, "wrong method", http.StatusMethodNotAllowed)
			return
		}
		if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", got)
			http.Error(w, "wrong content type", http.StatusUnsupportedMediaType)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		want := url.Values{
			"grant_type":    {"authorization_code"},
			"app_id":        {"cli_test"},
			"app_secret":    {"secret"},
			"code":          {"code-123"},
			"code_verifier": {"verifier-123"},
		}
		if !reflect.DeepEqual(r.PostForm, want) {
			t.Errorf("form = %#v, want %#v", r.PostForm, want)
			http.Error(w, "wrong form", http.StatusBadRequest)
			return
		}
		writeUserTokenResponse(t, w, "uat-1", "refresh-1")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := NewConfig("cli_test", "secret", WithBaseURL(srv.URL))
	tok, err := cfg.UserAuth().ExchangeCode(context.Background(), "code-123", "verifier-123")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if tok.AccessToken != "uat-1" || tok.RefreshToken != "refresh-1" {
		t.Fatalf("token = %+v", tok)
	}
}

func TestRefreshUsesNeoJSONContract(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/hduhelp-neo/open-apis/authen/refresh-access-token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
			http.Error(w, "wrong method", http.StatusMethodNotAllowed)
			return
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
			http.Error(w, "wrong content type", http.StatusUnsupportedMediaType)
			return
		}
		var got map[string]string
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode refresh request: %v", err)
			http.Error(w, "bad JSON", http.StatusBadRequest)
			return
		}
		want := map[string]string{
			"grant_type":    "refresh_token",
			"app_id":        "cli_test",
			"app_secret":    "secret",
			"refresh_token": "refresh-1",
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("JSON = %#v, want %#v", got, want)
			http.Error(w, "wrong JSON", http.StatusBadRequest)
			return
		}
		writeUserTokenResponse(t, w, "uat-2", "refresh-2")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := NewConfig("cli_test", "secret", WithBaseURL(srv.URL))
	tok, err := cfg.UserAuth().Refresh(context.Background(), "refresh-1")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tok.AccessToken != "uat-2" || tok.RefreshToken != "refresh-2" {
		t.Fatalf("token = %+v", tok)
	}
}

func writeUserTokenResponse(t *testing.T, w http.ResponseWriter, accessToken, refreshToken string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	err := json.NewEncoder(w).Encode(map[string]any{
		"code": 0,
		"msg":  "ok",
		"data": map[string]any{
			"accessToken":  accessToken,
			"refreshToken": refreshToken,
			"expiresIn":    7200,
			"scope":        "identity:staffid:read",
			"tenantKey":    "default",
			"tokenType":    "Bearer",
			"userId":       "user-1",
		},
	})
	if err != nil {
		t.Errorf("write token response: %v", err)
	}
}
