package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type stubPinger struct{ err error }

func (p stubPinger) Ping(context.Context) error { return p.err }

func TestHealth(t *testing.T) {
	rec := httptest.NewRecorder()
	NewRouter(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("healthz returned %d %q", rec.Code, rec.Body.String())
	}
}

func TestReady(t *testing.T) {
	for _, tc := range []struct {
		name string
		db   Pinger
		want int
	}{
		{name: "connected", db: stubPinger{}, want: http.StatusOK},
		{name: "disconnected", db: stubPinger{err: errors.New("offline")}, want: http.StatusServiceUnavailable},
		{name: "unconfigured", db: nil, want: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			NewRouter(tc.db).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if rec.Code != tc.want {
				t.Fatalf("readyz returned %d, want %d", rec.Code, tc.want)
			}
		})
	}
}
