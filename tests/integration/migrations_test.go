package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/justinangeloperez327/financial-ledger-api/db/migrations"
	"github.com/justinangeloperez327/financial-ledger-api/internal/ledger"
	"github.com/pressly/goose/v3"
)

func TestHardeningMigrationPreservesHistory(t *testing.T) {
	f := setup(t)
	schema := "upgrade_" + strings.ReplaceAll(ledger.ID(), "-", "")
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := testDB.Exec(f.ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := testDB.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg := testDB.Config()
	params := map[string]string{}
	for k, v := range cfg.ConnConfig.RuntimeParams {
		params[k] = v
	}
	params["search_path"] = schema
	cfg.ConnConfig.RuntimeParams = params
	db := stdlib.OpenDB(*cfg.ConnConfig)
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(f.ctx, 2); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s := &ledger.Store{DB: pool}
	l, err := s.CreateLedger(f.ctx, "Existing ledger", "tester")
	if err != nil {
		t.Fatal(err)
	}
	cash, err := s.CreateAccount(f.ctx, l.ID, "tester", ledger.NewAccount{Code: "1000", Name: "Cash", Kind: "asset", Currency: "AED"})
	if err != nil {
		t.Fatal(err)
	}
	equity, err := s.CreateAccount(f.ctx, l.ID, "tester", ledger.NewAccount{Code: "3000", Name: "Equity", Kind: "equity", Currency: "AED"})
	if err != nil {
		t.Fatal(err)
	}
	posting := ledger.Posting{Currency: "AED", Description: "Existing capital", Lines: []ledger.Line{{AccountID: cash.ID, Side: "debit", Amount: 100}, {AccountID: equity.ID, Side: "credit", Amount: 100}}}
	original, _, err := s.Post(f.ctx, l.ID, "original", "tester", "posted", posting, "")
	if err != nil {
		t.Fatal(err)
	}
	verify := func() {
		t.Helper()
		j, replay, err := s.Post(f.ctx, l.ID, "original", "tester", "posted", posting, "")
		if err != nil || !replay || j.ID != original.ID {
			t.Fatalf("migration changed retry result: %s %v %v", j.ID, replay, err)
		}
		b, err := s.Balances(f.ctx, l.ID, cash.ID)
		if err != nil || len(b) != 1 || b[0].Balance != "100" {
			t.Fatal("migration changed balances", b, err)
		}
		events, err := s.Audit(f.ctx, l.ID, 200, 0)
		if err != nil || len(events) != 4 {
			t.Fatal("migration changed audit history", events, err)
		}
	}
	if _, err = provider.Up(f.ctx); err != nil {
		t.Fatal(err)
	}
	verify()
	if _, err = provider.Down(f.ctx); err != nil {
		t.Fatal(err)
	}
	verify()
	if _, err = provider.Up(f.ctx); err != nil {
		t.Fatal(err)
	}
	verify()
	// The re-applied migration must still guard direct DML on the existing data.
	_, err = pool.Exec(f.ctx, "UPDATE accounts SET closed=true WHERE id=$1", cash.ID)
	requireCheckViolation(t, err)
}
