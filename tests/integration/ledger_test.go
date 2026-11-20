package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/justinangeloperez327/financial-ledger-api/db/migrations"
	"github.com/justinangeloperez327/financial-ledger-api/internal/auth"
	"github.com/justinangeloperez327/financial-ledger-api/internal/httpapi"
	"github.com/justinangeloperez327/financial-ledger-api/internal/ledger"
	"github.com/pressly/goose/v3"
)

var testDB *pgxpool.Pool

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		os.Exit(m.Run())
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		panic(err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.Files)
	if err != nil {
		panic(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err = provider.Up(ctx); err != nil {
		panic(err)
	}
	db.Close()
	testDB, err = pgxpool.New(ctx, dsn)
	if err != nil {
		panic(err)
	}
	code := m.Run()
	testDB.Close()
	os.Exit(code)
}

type fixture struct {
	s                  *ledger.Store
	ctx                context.Context
	lid                string
	cash, bank, equity ledger.Account
}

func setup(t *testing.T) *fixture {
	t.Helper()
	if testDB == nil {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	s := &ledger.Store{DB: testDB}
	l, err := s.CreateLedger(ctx, "Test ledger", "tester")
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{s: s, ctx: ctx, lid: l.ID}
	negative := false
	create := func(code, kind string, policy *bool) ledger.Account {
		a, e := s.CreateAccount(ctx, l.ID, "tester", ledger.NewAccount{Code: code, Name: code, Kind: kind, Currency: "AED", AllowNegative: policy})
		if e != nil {
			t.Fatal(e)
		}
		return a
	}
	f.cash = create("1000", "asset", &negative)
	f.bank = create("1100", "asset", &negative)
	f.equity = create("3000", "equity", nil)
	return f
}
func (f *fixture) funding(amount ledger.Amount) ledger.Posting {
	return ledger.Posting{Currency: "AED", Description: "Opening capital", Lines: []ledger.Line{{AccountID: f.cash.ID, Side: "debit", Amount: amount}, {AccountID: f.equity.ID, Side: "credit", Amount: amount}}}
}
func (f *fixture) fund(t *testing.T, amount ledger.Amount) ledger.Journal {
	t.Helper()
	j, _, err := f.s.Post(f.ctx, f.lid, ledger.ID(), "tester", "posted", f.funding(amount), "")
	if err != nil {
		t.Fatal(err)
	}
	return j
}
func (f *fixture) balance(t *testing.T, id string) string {
	t.Helper()
	b, err := f.s.Balances(f.ctx, f.lid, id)
	if err != nil {
		t.Fatal(err)
	}
	return b[0].Balance
}
func (f *fixture) count(t *testing.T) int {
	t.Helper()
	var n int
	if err := testDB.QueryRow(f.ctx, "SELECT count(*) FROM journals WHERE ledger_id=$1", f.lid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestPostingIdempotencyAndReversal(t *testing.T) {
	f := setup(t)
	key := ledger.ID()
	p := f.funding(10000)
	j, replay, err := f.s.Post(f.ctx, f.lid, key, "tester", "posted", p, "")
	if err != nil || replay {
		t.Fatal(j, replay, err)
	}
	again, replay, err := f.s.Post(f.ctx, f.lid, key, "tester", "posted", p, "")
	if err != nil || !replay || j.ID != again.ID {
		t.Fatal(again, replay, err)
	}
	a, _ := json.Marshal(j)
	b, _ := json.Marshal(again)
	if !bytes.Equal(a, b) {
		t.Fatalf("replay changed response: %s / %s", a, b)
	}
	p.Description = "changed"
	if _, _, err = f.s.Post(f.ctx, f.lid, key, "tester", "posted", p, ""); !errors.Is(err, ledger.ErrConflict) {
		t.Fatal(err)
	}
	reversalKey := ledger.ID()
	reverse, _, err := f.s.Post(f.ctx, f.lid, reversalKey, "tester", "reversed", ledger.Posting{Description: "Correction"}, j.ID)
	if err != nil || reverse.ReversalOf == nil {
		t.Fatal(reverse, err)
	}
	if f.balance(t, f.cash.ID) != "0" || f.balance(t, f.equity.ID) != "0" {
		t.Fatal("reversal did not net to zero")
	}
	again, replay, err = f.s.Post(f.ctx, f.lid, reversalKey, "tester", "reversed", ledger.Posting{Description: "Correction"}, j.ID)
	if err != nil || !replay || again.ID != reverse.ID {
		t.Fatal(again, replay, err)
	}
	if _, _, err = f.s.Post(f.ctx, f.lid, ledger.ID(), "tester", "reversed", ledger.Posting{Description: "Duplicate"}, j.ID); !errors.Is(err, ledger.ErrConflict) {
		t.Fatal(err)
	}
	if f.count(t) != 2 {
		t.Fatal("duplicate journal")
	}
	events, err := f.s.Audit(f.ctx, f.lid, 200, 0)
	if err != nil || len(events) != 6 {
		t.Fatal(events, err)
	}
}
func TestConcurrentRetryCreatesOneJournal(t *testing.T) {
	f := setup(t)
	const workers = 20
	key := ledger.ID()
	var wg sync.WaitGroup
	ids := make(chan string, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j, _, err := f.s.Post(f.ctx, f.lid, key, "tester", "posted", f.funding(100), "")
			ids <- j.ID
			errs <- err
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first string
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatal("different journals returned")
		}
	}
	if f.count(t) != 1 || f.balance(t, f.cash.ID) != "100" {
		t.Fatal("duplicate posting")
	}
}
func TestConcurrentWithdrawalsCannotOverdraw(t *testing.T) {
	f := setup(t)
	f.fund(t, 100)
	const workers = 20
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := ledger.Transfer{From: f.cash.ID, To: f.bank.ID, Currency: "AED", Amount: 10, Description: "withdraw"}.Posting()
			_, _, err := f.s.Post(f.ctx, f.lid, ledger.ID(), "tester", "transfer", p, "")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		} else if !errors.Is(err, ledger.ErrConflict) {
			t.Fatal(err)
		}
	}
	if success != 10 || f.balance(t, f.cash.ID) != "0" || f.balance(t, f.bank.ID) != "100" {
		t.Fatalf("success=%d cash=%s bank=%s", success, f.balance(t, f.cash.ID), f.balance(t, f.bank.ID))
	}
}
func TestOpposingTransfers(t *testing.T) {
	f := setup(t)
	f.fund(t, 2000)
	p := ledger.Transfer{From: f.cash.ID, To: f.bank.ID, Currency: "AED", Amount: 1000, Description: "seed bank"}.Posting()
	if _, _, err := f.s.Post(f.ctx, f.lid, ledger.ID(), "tester", "transfer", p, ""); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			from, to := f.cash.ID, f.bank.ID
			if i%2 == 0 {
				from, to = to, from
			}
			p := ledger.Transfer{From: from, To: to, Currency: "AED", Amount: 1, Description: "opposing"}.Posting()
			_, _, err := f.s.Post(f.ctx, f.lid, ledger.ID(), "tester", "transfer", p, "")
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if f.balance(t, f.cash.ID) != "1000" || f.balance(t, f.bank.ID) != "1000" {
		t.Fatal("money not conserved")
	}
}
func TestRejectedPostingIsAtomic(t *testing.T) {
	f := setup(t)
	other := setup(t)
	p := f.funding(100)
	p.Lines[1].AccountID = other.equity.ID
	if _, _, err := f.s.Post(f.ctx, f.lid, "cross-ledger", "tester", "posted", p, ""); err == nil {
		t.Fatal("cross-ledger accepted")
	}
	p = f.funding(100)
	p.Currency = "USD"
	if _, _, err := f.s.Post(f.ctx, f.lid, "wrong-currency", "tester", "posted", p, ""); err == nil {
		t.Fatal("currency mismatch accepted")
	}
	if err := f.s.CloseAccount(f.ctx, f.lid, f.cash.ID, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.s.Post(f.ctx, f.lid, "closed", "tester", "posted", f.funding(100), ""); err == nil {
		t.Fatal("closed account accepted")
	}
	if f.count(t) != 0 {
		t.Fatal("partial posting")
	}
}
func TestFailureAfterLineInsertionRollsBack(t *testing.T) {
	f := setup(t)
	_, err := testDB.Exec(f.ctx, `CREATE FUNCTION test_fail_idempotency() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected failure' USING ERRCODE='23514'; END; $$;
 CREATE TRIGGER test_failure BEFORE INSERT ON idempotency_keys FOR EACH ROW WHEN (NEW.key='fault-key') EXECUTE FUNCTION test_fail_idempotency();`)
	if err != nil {
		t.Fatal(err)
	}
	defer testDB.Exec(context.Background(), "DROP TRIGGER IF EXISTS test_failure ON idempotency_keys; DROP FUNCTION IF EXISTS test_fail_idempotency()")
	if _, _, err = f.s.Post(f.ctx, f.lid, "fault-key", "tester", "posted", f.funding(100), ""); err == nil {
		t.Fatal("expected injected failure")
	}
	if f.count(t) != 0 || f.balance(t, f.cash.ID) != "0" {
		t.Fatal("failure leaked financial writes")
	}
	if _, err = testDB.Exec(f.ctx, "DROP TRIGGER test_failure ON idempotency_keys; DROP FUNCTION test_fail_idempotency()"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.s.Post(f.ctx, f.lid, "fault-key", "tester", "posted", f.funding(100), ""); err != nil {
		t.Fatal("failed key could not retry", err)
	}
}
func TestDatabaseGuards(t *testing.T) {
	f := setup(t)
	j := f.fund(t, 100)
	for _, query := range []string{
		"UPDATE journals SET description='tampered' WHERE id=$1",
		"DELETE FROM journals WHERE id=$1",
		"UPDATE journal_lines SET amount=1 WHERE journal_id=$1",
		"DELETE FROM journal_lines WHERE journal_id=$1",
	} {
		if _, err := testDB.Exec(f.ctx, query, j.ID); err == nil {
			t.Fatal("mutation accepted", query)
		}
	}
	if _, err := testDB.Exec(f.ctx, "INSERT INTO journal_lines(journal_id,ledger_id,account_id,currency,side,amount) VALUES($1,$2,$3,'AED','debit',1)", j.ID, f.lid, f.bank.ID); err == nil {
		t.Fatal("append accepted")
	}
	tx, err := testDB.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := ledger.ID()
	if _, err = tx.Exec(f.ctx, "INSERT INTO journals(id,ledger_id,currency,description,actor) VALUES($1,$2,'AED','empty','tester')", id, f.lid); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err == nil {
		t.Fatal("empty journal committed")
	}
	tx, err = testDB.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	id = ledger.ID()
	if _, err = tx.Exec(f.ctx, "INSERT INTO journals(id,ledger_id,currency,description,actor) VALUES($1,$2,'AED','unbalanced','tester')", id, f.lid); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "INSERT INTO journal_lines(journal_id,ledger_id,account_id,currency,side,amount) VALUES($1,$2,$3,'AED','debit',10),($1,$2,$4,'AED','credit',9)", id, f.lid, f.cash.ID, f.equity.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err == nil {
		t.Fatal("unbalanced journal committed")
	}
	if f.count(t) != 1 {
		t.Fatal("guard leaked writes")
	}
}
func TestHTTPWorkflow(t *testing.T) {
	f := setup(t)
	v := auth.Verifier{Secret: []byte(strings.Repeat("h", 32)), Issuer: "test", Audience: "test"}
	now := time.Now()
	token, err := v.Sign(auth.Claims{Subject: "tester", Issuer: "test", Audience: "test", IssuedAt: now.Unix(), Expires: now.Add(time.Hour).Unix(), Ledgers: map[string]string{f.lid: "admin"}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.NewRouter(testDB, httpapi.API{Store: f.s, Verifier: v}))
	defer server.Close()
	request := func(method, path, key string, body any, status int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, err := http.NewRequest(method, server.URL+"/v1/ledgers/"+f.lid+path, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var data json.RawMessage
		if err = json.NewDecoder(resp.Body).Decode(&data); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != status {
			t.Fatalf("%s %s got %d expected %d: %s", method, path, resp.StatusCode, status, data)
		}
		return data
	}
	request("GET", "", "", nil, 200)
	raw := request("POST", "/journals", "opening", f.funding(1000), 201)
	var j ledger.Journal
	if err = json.Unmarshal(raw, &j); err != nil {
		t.Fatal(err)
	}
	request("POST", "/journals", "opening", f.funding(1000), 200)
	tr := ledger.Transfer{From: f.cash.ID, To: f.bank.ID, Currency: "AED", Amount: 250, Description: "Bank transfer"}
	request("POST", "/transfers", "move", tr, 201)
	request("GET", "/accounts/"+f.cash.ID+"/balance", "", nil, 200)
	request("GET", "/accounts/"+f.bank.ID+"/statement?limit=1", "", nil, 200)
	request("GET", "/trial-balance", "", nil, 200)
	request("GET", "/journals/"+j.ID, "", nil, 200)
	request("GET", "/journals?limit=1&offset=1", "", nil, 200)
	request("GET", "/audit-events", "", nil, 200)
	request("GET", "/accounts?limit=201", "", nil, 400)
	request("POST", "/journals", "", f.funding(1), 400)
	tr.Amount = 10000
	request("POST", "/transfers", "overdraw", tr, 409)
	if got := f.balance(t, f.cash.ID); got != "750" {
		t.Fatal(got)
	}
	request("POST", "/accounts/"+f.bank.ID+"/close", "", nil, 409)
	request("POST", "/transfers", "drain-bank", ledger.Transfer{From: f.bank.ID, To: f.cash.ID, Currency: "AED", Amount: 250, Description: "Drain bank before closure"}, 201)
	request("POST", "/accounts/"+f.bank.ID+"/close", "", nil, 200)
	request("POST", "/transfers", "closed", ledger.Transfer{From: f.cash.ID, To: f.bank.ID, Currency: "AED", Amount: 1, Description: "closed"}, 409)
}
