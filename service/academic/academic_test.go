package academic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hduhelp/hduhelp-neo-sdk-go/core"
)

func TestStudentInfoBuilderSendsStaffIDHeader(t *testing.T) {
	const staffID = "2025123456"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Staff-Id"); got != staffID {
			t.Errorf("X-Staff-Id = %q, want %q", got, staffID)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tenant-token" {
			t.Errorf("Authorization = %q, want tenant token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"code":0,"msg":"ok","data":{"staffId":"2025123456","staffName":"test"}}`)
	}))
	defer server.Close()

	service := NewService(core.NewConfig("", "",
		core.WithBaseURL(server.URL),
		core.WithEnableTokenCache(false),
	))
	resp, err := service.StudentInfo(
		context.Background(),
		NewStudentInfoReqBuilder().StaffID(staffID).Build(),
		core.WithTenantAccessToken("tenant-token"),
	)
	if err != nil {
		t.Fatalf("StudentInfo() error = %v", err)
	}
	if !resp.Success() {
		t.Fatalf("StudentInfo() response = code %d, msg %q", resp.Code, resp.Msg)
	}
}
