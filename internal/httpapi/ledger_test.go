package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/justinangeloperez327/financial-ledger-api/internal/auth"
	"github.com/justinangeloperez327/financial-ledger-api/internal/ledger"
)

func TestAuthorizationBeforeDatabase(t *testing.T) {
	id := ledger.ID()
	v := auth.Verifier{Secret: []byte(strings.Repeat("s", 32)), Issuer: "test", Audience: "test"}
	now := time.Now()
	c := auth.Claims{Subject: "reader", Issuer: "test", Audience: "test", IssuedAt: now.Unix(), Expires: now.Add(time.Hour).Unix(), Ledgers: map[string]string{id: "reader"}}
	token, _ := v.Sign(c)
	router := NewRouter(nil, API{Verifier: v})
	for _, tc := range []struct {
		method, path, token string
		status              int
	}{
		{"GET", "/v1/ledgers/" + id + "/accounts", "", 401},
		{"POST", "/v1/ledgers/" + id + "/journals", token, 403},
		{"GET", "/v1/ledgers/" + ledger.ID() + "/accounts", token, 403},
		{"GET", "/v1/ledgers/" + id + "/audit-events", token, 403},
		{"POST", "/v1/ledgers", token, 403},
		{"GET", "/v1/ledgers/invalid/accounts", token, 400},
	} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("%s %s: got %d want %d", tc.method, tc.path, w.Code, tc.status)
		}
	}
}
func TestStrictJSON(t *testing.T) {
	for _, body := range []string{`{"name":"ok","unknown":1}`, `{"name":"ok"} {}`, `{"name":`, strings.Repeat("x", 70000)} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		var in struct {
			Name string `json:"name"`
		}
		if decode(httptest.NewRecorder(), r, &in) == nil {
			t.Fatal("accepted malformed input")
		}
	}
}
