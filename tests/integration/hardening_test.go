package integration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/justinangeloperez327/financial-ledger-api/internal/ledger"
)

func requireCheckViolation(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("expected check violation, got %v", err)
	}
}

// Exercise the documented runtime grants, without owner or trigger-management rights.
func beginRuntimeTx(t *testing.T, f *fixture) pgx.Tx {
	t.Helper()
	role := pgx.Identifier{"runtime_" + strings.ReplaceAll(ledger.ID(), "-", "")}.Sanitize()
	_, err := testDB.Exec(f.ctx, "CREATE ROLE "+role+" NOLOGIN; "+
		"GRANT USAGE ON SCHEMA public TO "+role+"; "+
		"GRANT SELECT,INSERT ON ledgers,accounts,journals,journal_lines,idempotency_keys,audit_events TO "+role+"; "+
		"GRANT SELECT ON currencies TO "+role+"; "+
		"GRANT UPDATE(closed) ON accounts TO "+role+"; "+
		"GRANT USAGE,SELECT ON SEQUENCE audit_events_id_seq TO "+role)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := testDB.Exec(context.Background(), "DROP OWNED BY "+role+"; DROP ROLE "+role); err != nil {
			t.Error(err)
		}
	})
	tx, err := testDB.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "SET LOCAL ROLE "+role); err != nil {
		_ = tx.Rollback(f.ctx)
		t.Fatal(err)
	}
	return tx
}

func rawJournal(t *testing.T, f *fixture, tx pgx.Tx, reversal *string) string {
	t.Helper()
	id := ledger.ID()
	_, err := tx.Exec(f.ctx, "INSERT INTO journals(id,ledger_id,currency,description,reversal_of,actor) VALUES($1,$2,'AED','regression',$3,'tester')", id, f.lid, reversal)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func rawLines(t *testing.T, f *fixture, tx pgx.Tx, id string, debit, credit string, amount int64) {
	t.Helper()
	_, err := tx.Exec(f.ctx, "INSERT INTO journal_lines(journal_id,ledger_id,account_id,currency,side,amount) VALUES($1,$2,$3,'AED','debit',$5),($1,$2,$4,'AED','credit',$5)", id, f.lid, debit, credit, amount)
	if err != nil {
		t.Fatal(err)
	}
}

func TestLineValidationSurvivesConstraintFlush(t *testing.T) {
	for _, mode := range []string{"all_immediate", "header_immediate", "defer_again"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			tx := beginRuntimeTx(t, f)
			var err error
			defer tx.Rollback(context.Background())
			id := rawJournal(t, f, tx, nil)
			rawLines(t, f, tx, id, f.cash.ID, f.equity.ID, 100)
			constraint := "ALL"
			if mode == "header_immediate" {
				constraint = "balanced_journal"
			}
			if _, err = tx.Exec(f.ctx, "SET CONSTRAINTS "+constraint+" IMMEDIATE"); err != nil {
				t.Fatal(err)
			}
			if mode == "defer_again" {
				if _, err = tx.Exec(f.ctx, "SET CONSTRAINTS ALL DEFERRED"); err != nil {
					t.Fatal(err)
				}
			}
			_, err = tx.Exec(f.ctx, "INSERT INTO journal_lines(journal_id,ledger_id,account_id,currency,side,amount) VALUES($1,$2,$3,'AED','debit',1)", id, f.lid, f.bank.ID)
			if mode == "all_immediate" {
				requireCheckViolation(t, err)
			} else {
				if err != nil {
					t.Fatal(err)
				}
				requireCheckViolation(t, tx.Commit(f.ctx))
			}
			if err = tx.Rollback(f.ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
				t.Fatal(err)
			}
			if f.count(t) != 0 || f.balance(t, f.cash.ID) != "0" {
				t.Fatal("constraint failure leaked posting data")
			}
		})
	}
}

func TestBalancedAppendAfterConstraintFlush(t *testing.T) {
	f := setup(t)
	extra, err := f.s.CreateAccount(f.ctx, f.lid, "tester", ledger.NewAccount{Code: "4000", Name: "Liability", Kind: "liability", Currency: "AED"})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := testDB.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	id := rawJournal(t, f, tx, nil)
	rawLines(t, f, tx, id, f.cash.ID, f.equity.ID, 100)
	if _, err = tx.Exec(f.ctx, "SET CONSTRAINTS ALL IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	// A balanced multi-row statement remains valid with immediate constraints.
	rawLines(t, f, tx, id, f.bank.ID, extra.ID, 50)
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if f.count(t) != 1 || f.balance(t, f.bank.ID) != "50" {
		t.Fatal("valid balanced append was not preserved")
	}
}

func TestReversalValidationSurvivesConstraintFlush(t *testing.T) {
	f := setup(t)
	original := f.fund(t, 100)
	extra, err := f.s.CreateAccount(f.ctx, f.lid, "tester", ledger.NewAccount{Code: "4000", Name: "Liability", Kind: "liability", Currency: "AED"})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := testDB.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	id := rawJournal(t, f, tx, &original.ID)
	rawLines(t, f, tx, id, f.equity.ID, f.cash.ID, 100)
	if _, err = tx.Exec(f.ctx, "SET CONSTRAINTS balanced_journal IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	rawLines(t, f, tx, id, f.bank.ID, extra.ID, 1)
	requireCheckViolation(t, tx.Commit(f.ctx))
	if f.count(t) != 1 || f.balance(t, f.cash.ID) != "100" {
		t.Fatal("invalid reversal escaped rollback")
	}
}

func TestNegativePolicySurvivesConstraintFlush(t *testing.T) {
	f := setup(t)
	negative := false
	extra, err := f.s.CreateAccount(f.ctx, f.lid, "tester", ledger.NewAccount{Code: "1200", Name: "Protected asset", Kind: "asset", Currency: "AED", AllowNegative: &negative})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := testDB.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	id := rawJournal(t, f, tx, nil)
	rawLines(t, f, tx, id, f.cash.ID, f.equity.ID, 100)
	if _, err = tx.Exec(f.ctx, "SET CONSTRAINTS balanced_journal IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	rawLines(t, f, tx, id, f.bank.ID, extra.ID, 1)
	requireCheckViolation(t, tx.Commit(f.ctx))
	if f.count(t) != 0 || f.balance(t, extra.ID) != "0" {
		t.Fatal("negative balance escaped rollback")
	}
}

func TestClosureRequiresZeroBalance(t *testing.T) {
	f := setup(t)
	f.fund(t, 100)
	before, err := f.s.Audit(f.ctx, f.lid, 200, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.CloseAccount(f.ctx, f.lid, f.cash.ID, "tester"); !errors.Is(err, ledger.ErrConflict) {
		t.Fatalf("funded closure: %v", err)
	}
	_, err = testDB.Exec(f.ctx, "UPDATE accounts SET closed=true WHERE id=$1", f.cash.ID)
	requireCheckViolation(t, err)
	after, err := f.s.Audit(f.ctx, f.lid, 200, 0)
	if err != nil || len(after) != len(before) {
		t.Fatal("failed closure changed audit history", err)
	}
	p := ledger.Transfer{From: f.cash.ID, To: f.bank.ID, Currency: "AED", Amount: 100, Description: "Drain cash"}.Posting()
	if _, _, err = f.s.Post(f.ctx, f.lid, "drain", "tester", "transfer", p, ""); err != nil {
		t.Fatal("rejected closure froze account", err)
	}
	if err = f.s.CloseAccount(f.ctx, f.lid, f.cash.ID, "tester"); err != nil {
		t.Fatal(err)
	}
	if err = f.s.CloseAccount(f.ctx, f.lid, f.cash.ID, "tester"); !errors.Is(err, ledger.ErrNotFound) {
		t.Fatal("repeated closure", err)
	}
	after, err = f.s.Audit(f.ctx, f.lid, 200, 0)
	if err != nil {
		t.Fatal(err)
	}
	closures := 0
	for _, e := range after {
		if e.Action == "account.closed" {
			closures++
		}
	}
	if closures != 1 {
		t.Fatalf("got %d closure audit events", closures)
	}
}

func TestClosureAllowsZeroNetHistory(t *testing.T) {
	f := setup(t)
	j := f.fund(t, 100)
	if _, _, err := f.s.Post(f.ctx, f.lid, "correction", "tester", "reversed", ledger.Posting{Description: "Correction"}, j.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{f.cash.ID, f.equity.ID} {
		if err := f.s.CloseAccount(f.ctx, f.lid, id, "tester"); err != nil {
			t.Fatal(err)
		}
	}
	if f.count(t) != 2 {
		t.Fatal("closure changed financial history")
	}
}

func TestClosureRejectsNegativeBalance(t *testing.T) {
	f := setup(t)
	extra, err := f.s.CreateAccount(f.ctx, f.lid, "tester", ledger.NewAccount{Code: "1200", Name: "Overdrawn", Kind: "asset", Currency: "AED"})
	if err != nil {
		t.Fatal(err)
	}
	p := ledger.Transfer{From: extra.ID, To: f.bank.ID, Currency: "AED", Amount: 10, Description: "Overdraw permitted account"}.Posting()
	if _, _, err = f.s.Post(f.ctx, f.lid, "overdraw", "tester", "transfer", p, ""); err != nil {
		t.Fatal(err)
	}
	if err = f.s.CloseAccount(f.ctx, f.lid, extra.ID, "tester"); !errors.Is(err, ledger.ErrConflict) {
		t.Fatal(err)
	}
	_, err = testDB.Exec(f.ctx, "UPDATE accounts SET closed=true WHERE id=$1", extra.ID)
	requireCheckViolation(t, err)
}

func waitForAccountLock(t *testing.T, f *fixture) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var waiting bool
		err := testDB.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE $1)", "%FROM accounts WHERE ledger_id=$1 AND id=$2 FOR UPDATE").Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("competing operation did not wait for account lock")
			return
		case <-tick.C:
		}
	}
}

func TestClosureSerializesWithPosting(t *testing.T) {
	for _, first := range []string{"posting", "closure"} {
		t.Run(first, func(t *testing.T) {
			f := setup(t)
			tx, err := testDB.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if _, err = tx.Exec(f.ctx, "SELECT id FROM accounts WHERE id=$1 FOR UPDATE", f.cash.ID); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			if first == "posting" {
				id := rawJournal(t, f, tx, nil)
				rawLines(t, f, tx, id, f.cash.ID, f.equity.ID, 100)
				go func() { result <- f.s.CloseAccount(f.ctx, f.lid, f.cash.ID, "tester") }()
			} else {
				if _, err = tx.Exec(f.ctx, "UPDATE accounts SET closed=true WHERE id=$1", f.cash.ID); err != nil {
					t.Fatal(err)
				}
				go func() {
					_, _, err := f.s.Post(f.ctx, f.lid, "racing-post", "tester", "posted", f.funding(100), "")
					result <- err
				}()
			}
			waitForAccountLock(t, f)
			if err = tx.Commit(f.ctx); err != nil {
				t.Fatal(err)
			}
			if err = <-result; !errors.Is(err, ledger.ErrConflict) {
				t.Fatalf("competing %s: %v", first, err)
			}
			var closed bool
			if err = testDB.QueryRow(f.ctx, "SELECT closed FROM accounts WHERE id=$1", f.cash.ID).Scan(&closed); err != nil {
				t.Fatal(err)
			}
			if first == "posting" && (closed || f.balance(t, f.cash.ID) != "100") {
				t.Fatal("closure ignored committed posting")
			}
			if first == "closure" && (!closed || f.balance(t, f.cash.ID) != "0") {
				t.Fatal("posting ignored committed closure")
			}
		})
	}
}

func TestUnicodeTextRoundTripAndKeyByteLimit(t *testing.T) {
	f := setup(t)
	name := strings.Repeat("ع", 120)
	l, err := f.s.CreateLedger(f.ctx, name, "tester")
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.s.GetLedger(f.ctx, l.ID)
	if err != nil || got.Name != name {
		t.Fatal("ledger name round trip", err)
	}
	a, err := f.s.CreateAccount(f.ctx, f.lid, "tester", ledger.NewAccount{Code: strings.Repeat("ع", 40), Name: name, Kind: "asset", Currency: "AED"})
	if err != nil || a.Name != name {
		t.Fatal("account Unicode fields", err)
	}
	p := f.funding(100)
	p.Description = strings.Repeat("ع", 500)
	key := strings.Repeat("ع", 64) // 128 bytes, matching the opaque-key contract.
	j, replay, err := f.s.Post(f.ctx, f.lid, key, "tester", "posted", p, "")
	if err != nil || replay {
		t.Fatal(j, replay, err)
	}
	saved, err := f.s.Journal(f.ctx, f.lid, j.ID)
	if err != nil || saved.Description != p.Description {
		t.Fatal("description round trip", err)
	}
	if _, _, err = f.s.Post(f.ctx, f.lid, strings.Repeat("ع", 65), "tester", "posted", p, ""); !errors.Is(err, ledger.ErrInvalid) {
		t.Fatal("oversized key was accepted", err)
	}
	if f.count(t) != 1 {
		t.Fatal("oversized key created a journal")
	}
}
