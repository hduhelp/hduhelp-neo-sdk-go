package hduhelp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hduhelp/hduhelp-neo-sdk-go"
	"github.com/hduhelp/hduhelp-neo-sdk-go/service/academic"
)

// fakeGateway serves the tenant token endpoint plus one academic endpoint,
// recording the Authorization header seen on the academic call.
func fakeGateway(t *testing.T, gotAuth *string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/hduhelp-neo/open-apis/auth/v3/tenant_access_token/internal",
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"tenantAccessToken":"tat-xyz","expire":7200}}`))
		})
	mux.HandleFunc("/hduhelp-neo/academic/schedule",
		func(w http.ResponseWriter, r *http.Request) {
			*gotAuth = r.Header.Get("Authorization")
			w.Header().Set("X-Request-Id", "req-123")
			w.Header().Set("Content-Type", "application/json")
			resp := map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": []map[string]any{{"courseName": "Compilers"}},
			}
			_ = json.NewEncoder(w).Encode(resp)
		})
	return httptest.NewServer(mux)
}

func TestClientInjectsTenantTokenAndParsesResponse(t *testing.T) {
	var gotAuth string
	srv := fakeGateway(t, &gotAuth)
	defer srv.Close()

	client := hduhelp.NewClient("cli_test", "secret", hduhelp.WithBaseURL(srv.URL))

	req := academic.NewScheduleReqBuilder().
		SchoolYear("2025-2026").
		Semester(1).
		Week(3).
		Build()

	resp, err := client.Academic.Schedule(context.Background(), req)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if !resp.Success() {
		t.Fatalf("expected success, got code=%d msg=%q", resp.Code, resp.Msg)
	}
	if gotAuth != "Bearer tat-xyz" {
		t.Fatalf("expected auto tenant token, got Authorization=%q", gotAuth)
	}
	if resp.RequestID() != "req-123" {
		t.Fatalf("expected request id req-123, got %q", resp.RequestID())
	}
	if len(resp.Data) != 1 || resp.Data[0].CourseName == nil || *resp.Data[0].CourseName != "Compilers" {
		t.Fatalf("unexpected typed data: %+v", resp.Data)
	}
}

func TestPerRequestUserAccessTokenOverride(t *testing.T) {
	var gotAuth string
	srv := fakeGateway(t, &gotAuth)
	defer srv.Close()

	client := hduhelp.NewClient("cli_test", "secret", hduhelp.WithBaseURL(srv.URL))
	req := academic.NewScheduleReqBuilder().SchoolYear("2025-2026").Build()

	_, err := client.Academic.Schedule(context.Background(), req,
		hduhelp.WithUserAccessToken("uat-override"))
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if gotAuth != "Bearer uat-override" {
		t.Fatalf("expected per-request UAT override, got %q", gotAuth)
	}
}

func TestPATClientOption(t *testing.T) {
	var gotAuth string
	srv := fakeGateway(t, &gotAuth)
	defer srv.Close()

	// A client-level PAT authenticates every call without touching the token endpoint.
	client := hduhelp.NewClient("", "", hduhelp.WithBaseURL(srv.URL), hduhelp.WithPAT("hduhelp_pat_abc"))
	req := academic.NewScheduleReqBuilder().Build()
	if _, err := client.Academic.Schedule(context.Background(), req); err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if gotAuth != "Bearer hduhelp_pat_abc" {
		t.Fatalf("expected PAT bearer, got %q", gotAuth)
	}
}
