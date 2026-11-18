package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/justinangeloperez327/financial-ledger-api/internal/auth"
	"github.com/justinangeloperez327/financial-ledger-api/internal/ledger"
	"github.com/justinangeloperez327/financial-ledger-api/internal/platform/database/queries"
)

type claimsKey struct{}
type API struct {
	Store    *ledger.Store
	Verifier auth.Verifier
}

func (a API) Routes(r chi.Router) {
	r.Route("/v1", func(r chi.Router) {
		r.Use(a.authenticate)
		r.Post("/ledgers", a.createLedger)
		r.Get("/currencies", func(w http.ResponseWriter, r *http.Request) {
			v, e := queries.New(a.Store.DB).ListCurrencies(r.Context())
			respond(w, r, 200, map[string]any{"data": v}, e)
		})
		r.Route("/ledgers/{ledgerID}", func(r chi.Router) {
			r.Use(a.authorize)
			r.Get("/", func(w http.ResponseWriter, r *http.Request) {
				v, e := a.Store.GetLedger(r.Context(), lid(r))
				respond(w, r, 200, v, e)
			})
			r.Get("/accounts", a.accounts)
			r.Post("/accounts", a.createAccount)
			r.Post("/accounts/{accountID}/close", a.closeAccount)
			r.Get("/accounts/{accountID}/balance", a.balance)
			r.Get("/accounts/{accountID}/statement", a.statement)
			r.Post("/journals", a.post)
			r.Get("/journals", a.journals)
			r.Get("/journals/{journalID}", a.journal)
			r.Post("/journals/{journalID}/reverse", a.reverse)
			r.Post("/transfers", a.transfer)
			r.Get("/trial-balance", a.trialBalance)
			r.Get("/audit-events", a.audit)
		})
	})
}
func (a API) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			failure(w, r, 401, "unauthorized")
			return
		}
		c, err := a.Verifier.Verify(strings.TrimPrefix(header, "Bearer "), time.Now())
		if err != nil {
			failure(w, r, 401, "unauthorized")
			return
		}
		ctx, cancel := context.WithTimeout(context.WithValue(r.Context(), claimsKey{}, c), 10*time.Second)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func (a API) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ledger.ValidID(lid(r)) {
			failure(w, r, 400, "invalid_request")
			return
		}
		c := claims(r)
		admin := strings.HasSuffix(r.URL.Path, "/close") || strings.HasSuffix(r.URL.Path, "/audit-events")
		if !c.Can(lid(r), r.Method != "GET", admin) {
			failure(w, r, 403, "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func claims(r *http.Request) auth.Claims { return r.Context().Value(claimsKey{}).(auth.Claims) }
func lid(r *http.Request) string         { return chi.URLParam(r, "ledgerID") }
func resource(r *http.Request, key string) (string, error) {
	id := chi.URLParam(r, key)
	if !ledger.ValidID(id) {
		return "", ledger.ErrInvalid
	}
	return id, nil
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		return ledger.ErrInvalid
	}
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return ledger.ErrInvalid
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return ledger.ErrInvalid
	}
	return nil
}
func page(r *http.Request) (int, int, error) {
	limit, offset := 50, 0
	var err error
	if s := r.URL.Query().Get("limit"); s != "" {
		limit, err = strconv.Atoi(s)
		if err != nil {
			return 0, 0, ledger.ErrInvalid
		}
	}
	if s := r.URL.Query().Get("offset"); s != "" {
		offset, err = strconv.Atoi(s)
		if err != nil {
			return 0, 0, ledger.ErrInvalid
		}
	}
	if limit < 1 || limit > 200 || offset < 0 || offset > 1000000 {
		return 0, 0, ledger.ErrInvalid
	}
	return limit, offset, nil
}
func failure(w http.ResponseWriter, r *http.Request, status int, code string) {
	if status == 401 {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "request_id": middleware.GetReqID(r.Context())}})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func respond(w http.ResponseWriter, r *http.Request, status int, v any, err error) {
	if err == nil {
		writeJSON(w, status, v)
		return
	}
	switch {
	case errors.Is(err, ledger.ErrInvalid):
		failure(w, r, 400, "invalid_request")
	case errors.Is(err, ledger.ErrNotFound):
		failure(w, r, 404, "not_found")
	case errors.Is(err, ledger.ErrConflict):
		failure(w, r, 409, "conflict")
	case errors.Is(err, context.DeadlineExceeded):
		failure(w, r, 503, "unavailable")
	default:
		slog.ErrorContext(r.Context(), "request failed", "request_id", middleware.GetReqID(r.Context()), "error", err)
		failure(w, r, 500, "internal_error")
	}
}
func (a API) createLedger(w http.ResponseWriter, r *http.Request) {
	if !claims(r).Admin {
		failure(w, r, 403, "forbidden")
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(w, r, &in); err != nil {
		respond(w, r, 0, nil, err)
		return
	}
	v, e := a.Store.CreateLedger(r.Context(), in.Name, claims(r).Subject)
	respond(w, r, 201, v, e)
}
func (a API) createAccount(w http.ResponseWriter, r *http.Request) {
	var in ledger.NewAccount
	if err := decode(w, r, &in); err != nil {
		respond(w, r, 0, nil, err)
		return
	}
	v, e := a.Store.CreateAccount(r.Context(), lid(r), claims(r).Subject, in)
	respond(w, r, 201, v, e)
}
func (a API) accounts(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := page(r)
	if err != nil {
		respond(w, r, 0, nil, err)
		return
	}
	v, e := a.Store.Accounts(r.Context(), lid(r), limit, offset)
	respond(w, r, 200, map[string]any{"data": v, "limit": limit, "offset": offset}, e)
}
func (a API) closeAccount(w http.ResponseWriter, r *http.Request) {
	id, err := resource(r, "accountID")
	if err == nil {
		err = a.Store.CloseAccount(r.Context(), lid(r), id, claims(r).Subject)
	}
	respond(w, r, 200, map[string]string{"status": "closed"}, err)
}
func (a API) balance(w http.ResponseWriter, r *http.Request) {
	id, err := resource(r, "accountID")
	if err != nil {
		respond(w, r, 0, nil, err)
		return
	}
	v, e := a.Store.Balances(r.Context(), lid(r), id)
	if e != nil {
		respond(w, r, 0, nil, e)
		return
	}
	respond(w, r, 200, v[0], nil)
}
func (a API) trialBalance(w http.ResponseWriter, r *http.Request) {
	v, e := a.Store.Balances(r.Context(), lid(r), "")
	respond(w, r, 200, map[string]any{"data": v}, e)
}
func (a API) statement(w http.ResponseWriter, r *http.Request) {
	id, err := resource(r, "accountID")
	if err != nil {
		respond(w, r, 0, nil, err)
		return
	}
	limit, offset, err := page(r)
	if err != nil {
		respond(w, r, 0, nil, err)
		return
	}
	v, e := a.Store.Statement(r.Context(), lid(r), id, limit, offset)
	respond(w, r, 200, map[string]any{"data": v, "limit": limit, "offset": offset}, e)
}
func (a API) post(w http.ResponseWriter, r *http.Request) {
	var in ledger.Posting
	if err := decode(w, r, &in); err != nil {
		respond(w, r, 0, nil, err)
		return
	}
	a.posting(w, r, "posted", in, "")
}
func (a API) transfer(w http.ResponseWriter, r *http.Request) {
	var in ledger.Transfer
	if err := decode(w, r, &in); err != nil {
		respond(w, r, 0, nil, err)
		return
	}
	a.posting(w, r, "transfer", in.Posting(), "")
}
func (a API) reverse(w http.ResponseWriter, r *http.Request) {
	id, err := resource(r, "journalID")
	if err != nil {
		respond(w, r, 0, nil, err)
		return
	}
	var in struct {
		Description string `json:"description"`
	}
	if err = decode(w, r, &in); err != nil {
		respond(w, r, 0, nil, err)
		return
	}
	if !ledger.Text(in.Description, 500) {
		respond(w, r, 0, nil, ledger.ErrInvalid)
		return
	}
	a.posting(w, r, "reversed", ledger.Posting{Description: in.Description}, id)
}
func (a API) posting(w http.ResponseWriter, r *http.Request, operation string, in ledger.Posting, reversal string) {
	v, replay, e := a.Store.Post(r.Context(), lid(r), r.Header.Get("Idempotency-Key"), claims(r).Subject, operation, in, reversal)
	status := 201
	if replay {
		status = 200
		w.Header().Set("Idempotency-Replayed", "true")
	}
	respond(w, r, status, v, e)
}
func (a API) journals(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := page(r)
	if err != nil {
		respond(w, r, 0, nil, err)
		return
	}
	v, e := a.Store.Journals(r.Context(), lid(r), limit, offset)
	respond(w, r, 200, map[string]any{"data": v, "limit": limit, "offset": offset}, e)
}
func (a API) journal(w http.ResponseWriter, r *http.Request) {
	id, err := resource(r, "journalID")
	if err != nil {
		respond(w, r, 0, nil, err)
		return
	}
	v, e := a.Store.Journal(r.Context(), lid(r), id)
	respond(w, r, 200, v, e)
}
func (a API) audit(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := page(r)
	if err != nil {
		respond(w, r, 0, nil, err)
		return
	}
	v, e := a.Store.Audit(r.Context(), lid(r), limit, offset)
	respond(w, r, 200, map[string]any{"data": v, "limit": limit, "offset": offset}, e)
}
