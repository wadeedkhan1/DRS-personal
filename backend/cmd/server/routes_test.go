package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The invite-link routes sit underneath patterns that already existed:
//
//	GET    /api/devices/                     -> GetDevice
//	DELETE /api/devices/                     -> DeleteDevice
//	GET    /api/devices/enrollment-tokens    -> ListEnrollmentTokens      (new)
//	DELETE /api/devices/enrollment-token/    -> RevokeEnrollmentToken     (new)
//
// Go 1.22's ServeMux resolves this by specificity, but it *panics at registration* on a
// genuine conflict — which would mean the server fails to boot rather than failing a
// build. This pins the precedence so a future route cannot silently swallow another, and
// catches the panic here instead of in production.
func TestEnrollmentTokenRoutePrecedence(t *testing.T) {
	mark := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(name)) }
	}

	mux := http.NewServeMux()
	// Registered in the same order main.go registers them.
	mux.HandleFunc("POST /api/devices/enroll", mark("EnrollDevice"))
	mux.HandleFunc("GET /api/enroll/agent", mark("DownloadAgent"))
	mux.HandleFunc("GET /api/enroll/availability", mark("AgentAvailability"))
	mux.Handle("GET /api/devices", mark("ListDevices"))
	mux.Handle("GET /api/devices/", mark("GetDevice"))
	mux.Handle("POST /api/devices/enrollment-token", mark("GenerateEnrollmentToken"))
	mux.Handle("GET /api/devices/enrollment-tokens", mark("ListEnrollmentTokens"))
	mux.Handle("DELETE /api/devices/enrollment-token/", mark("RevokeEnrollmentToken"))
	mux.Handle("DELETE /api/devices/", mark("DeleteDevice"))

	for _, tc := range []struct{ method, path, want string }{
		{"GET", "/api/devices", "ListDevices"},
		{"GET", "/api/devices/9f1c0f1e-0000-0000-0000-000000000001", "GetDevice"},
		{"GET", "/api/devices/enrollment-tokens", "ListEnrollmentTokens"},
		{"POST", "/api/devices/enrollment-token", "GenerateEnrollmentToken"},
		{"DELETE", "/api/devices/enrollment-token/9f1c0f1e-0000-0000-0000-000000000002", "RevokeEnrollmentToken"},
		{"DELETE", "/api/devices/9f1c0f1e-0000-0000-0000-000000000003", "DeleteDevice"},
		{"POST", "/api/devices/enroll", "EnrollDevice"},
		{"GET", "/api/enroll/agent", "DownloadAgent"},
		{"GET", "/api/enroll/availability", "AgentAvailability"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if got := rec.Body.String(); got != tc.want {
				t.Errorf("routed to %q, want %q (status %d)", got, tc.want, rec.Code)
			}
		})
	}
}
