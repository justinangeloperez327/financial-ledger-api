package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ DB *pgxpool.Pool }

func dbError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var e *pgconn.PgError
	if errors.As(err, &e) {
		switch e.Code {
		case "23505", "23514", "40001", "40P01", "55P03":
			return ErrConflict
		case "23503", "22P02", "22003":
			return ErrInvalid
		}
	}
	return err
}
func audit(ctx context.Context, tx pgx.Tx, lid, actor, action, id string) error {
	_, err := tx.Exec(ctx, "INSERT INTO audit_events(ledger_id,actor,action,resource_id) VALUES($1,$2,$3,$4)", lid, actor, action, id)
	return err
}
func (s *Store) CreateLedger(ctx context.Context, name, actor string) (Ledger, error) {
	if !Text(name, 120) {
		return Ledger{}, ErrInvalid
	}
	l := Ledger{ID: ID(), Name: name}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return l, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, "INSERT INTO ledgers(id,name) VALUES($1,$2) RETURNING created_at", l.ID, l.Name).Scan(&l.CreatedAt)
	if err == nil {
		err = audit(ctx, tx, l.ID, actor, "ledger.created", l.ID)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return l, dbError(err)
}
func (s *Store) GetLedger(ctx context.Context, id string) (Ledger, error) {
	var l Ledger
	err := s.DB.QueryRow(ctx, "SELECT id::text,name,created_at FROM ledgers WHERE id=$1", id).Scan(&l.ID, &l.Name, &l.CreatedAt)
	return l, dbError(err)
}

const accountCols = "id::text,ledger_id::text,code,name,kind,currency,allow_negative,closed,created_at"

func scanAccount(row pgx.Row) (Account, error) {
	var a Account
	err := row.Scan(&a.ID, &a.LedgerID, &a.Code, &a.Name, &a.Kind, &a.Currency, &a.AllowNegative, &a.Closed, &a.CreatedAt)
	return a, err
}
func (s *Store) CreateAccount(ctx context.Context, lid, actor string, in NewAccount) (Account, error) {
	if err := in.Validate(); err != nil {
		return Account{}, err
	}
	negative := true
	if in.AllowNegative != nil {
		negative = *in.AllowNegative
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback(ctx)
	a, err := scanAccount(tx.QueryRow(ctx, "INSERT INTO accounts(id,ledger_id,code,name,kind,currency,allow_negative) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING "+accountCols, ID(), lid, in.Code, in.Name, in.Kind, in.Currency, negative))
	if err == nil {
		err = audit(ctx, tx, lid, actor, "account.created", a.ID)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return a, dbError(err)
}
func (s *Store) Accounts(ctx context.Context, lid string, limit, offset int) ([]Account, error) {
	rows, err := s.DB.Query(ctx, "SELECT "+accountCols+" FROM accounts WHERE ledger_id=$1 ORDER BY code,id LIMIT $2 OFFSET $3", lid, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) CloseAccount(ctx context.Context, lid, id, actor string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, "UPDATE accounts SET closed=true WHERE ledger_id=$1 AND id=$2 AND NOT closed", lid, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err == nil {
		err = audit(ctx, tx, lid, actor, "account.closed", id)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return dbError(err)
}

const journalCols = "id::text,ledger_id::text,currency,description,reversal_of::text,actor,created_at"

type querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func getJournal(ctx context.Context, q querier, lid, id string) (Journal, error) {
	var j Journal
	err := q.QueryRow(ctx, "SELECT "+journalCols+" FROM journals WHERE ledger_id=$1 AND id=$2", lid, id).Scan(&j.ID, &j.LedgerID, &j.Currency, &j.Description, &j.ReversalOf, &j.Actor, &j.CreatedAt)
	if err != nil {
		return j, dbError(err)
	}
	rows, err := q.Query(ctx, "SELECT account_id::text,side,amount FROM journal_lines WHERE journal_id=$1 ORDER BY account_id", id)
	if err != nil {
		return j, err
	}
	defer rows.Close()
	j.Lines = []Line{}
	for rows.Next() {
		var l Line
		if err := rows.Scan(&l.AccountID, &l.Side, &l.Amount); err != nil {
			return j, err
		}
		j.Lines = append(j.Lines, l)
	}
	return j, rows.Err()
}
func (s *Store) Journal(ctx context.Context, lid, id string) (Journal, error) {
	return getJournal(ctx, s.DB, lid, id)
}
func (s *Store) Journals(ctx context.Context, lid string, limit, offset int) ([]Journal, error) {
	rows, err := s.DB.Query(ctx, "SELECT "+journalCols+" FROM journals WHERE ledger_id=$1 ORDER BY created_at,id LIMIT $2 OFFSET $3", lid, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Journal{}
	for rows.Next() {
		var j Journal
		if err := rows.Scan(&j.ID, &j.LedgerID, &j.Currency, &j.Description, &j.ReversalOf, &j.Actor, &j.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// Post serializes retries before locking accounts. The key, audit and posting share one commit.
func (s *Store) Post(ctx context.Context, lid, key, actor, operation string, p Posting, reversal string) (Journal, bool, error) {
	if !Text(key, 128) {
		return Journal{}, false, ErrInvalid
	}
	canonical, err := json.Marshal(struct {
		Operation string
		Posting   Posting
		Reversal  string
	}{operation, p, reversal})
	if err != nil {
		return Journal{}, false, err
	}
	hash := sha256.Sum256(canonical)
	fingerprint := hex.EncodeToString(hash[:])
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return Journal{}, false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='5s'"); err != nil {
		return Journal{}, false, err
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", lid+":"+key); err != nil {
		return Journal{}, false, dbError(err)
	}
	var savedHash, jid string
	err = tx.QueryRow(ctx, "SELECT fingerprint,journal_id::text FROM idempotency_keys WHERE ledger_id=$1 AND key=$2", lid, key).Scan(&savedHash, &jid)
	if err == nil {
		if savedHash != fingerprint {
			return Journal{}, false, ErrConflict
		}
		j, err := getJournal(ctx, tx, lid, jid)
		return j, true, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Journal{}, false, err
	}
	if reversal != "" {
		original, err := getJournal(ctx, tx, lid, reversal)
		if err != nil {
			return Journal{}, false, err
		}
		if original.ReversalOf != nil {
			return Journal{}, false, ErrConflict
		}
		p.Currency = original.Currency
		p.Lines = original.Lines
		for i := range p.Lines {
			if p.Lines[i].Side == "debit" {
				p.Lines[i].Side = "credit"
			} else {
				p.Lines[i].Side = "debit"
			}
		}
	}
	if err = p.Validate(); err != nil {
		return Journal{}, false, err
	}
	sorted := append([]Line(nil), p.Lines...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].AccountID < sorted[j].AccountID })
	for _, line := range sorted {
		a, err := scanAccount(tx.QueryRow(ctx, "SELECT "+accountCols+" FROM accounts WHERE ledger_id=$1 AND id=$2 FOR UPDATE", lid, line.AccountID))
		if err != nil {
			return Journal{}, false, dbError(err)
		}
		if a.Closed || a.Currency != p.Currency || (operation == "transfer" && a.Kind != "asset") {
			return Journal{}, false, ErrConflict
		}
		if !a.AllowNegative {
			normal := "credit"
			if a.Kind == "asset" || a.Kind == "expense" {
				normal = "debit"
			}
			var valid bool
			err = tx.QueryRow(ctx, "SELECT coalesce(sum(CASE WHEN side=$2 THEN amount::numeric ELSE -amount::numeric END),0)+$3::numeric>=0 FROM journal_lines WHERE account_id=$1", a.ID, normal, delta(line, normal)).Scan(&valid)
			if err != nil {
				return Journal{}, false, err
			}
			if !valid {
				return Journal{}, false, ErrConflict
			}
		}
	}
	j := Journal{ID: ID(), LedgerID: lid, Currency: p.Currency, Description: p.Description, Actor: actor, Lines: sorted}
	if reversal != "" {
		j.ReversalOf = &reversal
	}
	err = tx.QueryRow(ctx, "INSERT INTO journals(id,ledger_id,currency,description,reversal_of,actor) VALUES($1,$2,$3,$4,$5,$6) RETURNING created_at", j.ID, lid, j.Currency, j.Description, j.ReversalOf, actor).Scan(&j.CreatedAt)
	if err != nil {
		return Journal{}, false, dbError(err)
	}
	for _, line := range sorted {
		if _, err = tx.Exec(ctx, "INSERT INTO journal_lines(journal_id,ledger_id,account_id,currency,side,amount) VALUES($1,$2,$3,$4,$5,$6)", j.ID, lid, line.AccountID, j.Currency, line.Side, int64(line.Amount)); err != nil {
			return Journal{}, false, dbError(err)
		}
	}
	if _, err = tx.Exec(ctx, "INSERT INTO idempotency_keys(ledger_id,key,fingerprint,journal_id) VALUES($1,$2,$3,$4)", lid, key, fingerprint, j.ID); err != nil {
		return Journal{}, false, dbError(err)
	}
	if err = audit(ctx, tx, lid, actor, "journal."+operation, j.ID); err != nil {
		return Journal{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Journal{}, false, dbError(err)
	}
	// Stable ordering for original responses and replays.
	sort.Slice(j.Lines, func(i, k int) bool { return j.Lines[i].AccountID < j.Lines[k].AccountID })
	return j, false, nil
}
func delta(l Line, normal string) string {
	n := int64(l.Amount)
	if l.Side != normal {
		n = -n
	}
	return fmt.Sprint(n)
}
func (s *Store) Balances(ctx context.Context, lid, id string) ([]Balance, error) {
	rows, err := s.DB.Query(ctx, `SELECT a.id::text,a.code,a.kind,a.currency,
 coalesce(sum(l.amount) FILTER(WHERE l.side='debit'),0)::text,
 coalesce(sum(l.amount) FILTER(WHERE l.side='credit'),0)::text,
 coalesce(sum(CASE WHEN l.side=CASE WHEN a.kind IN ('asset','expense') THEN 'debit' ELSE 'credit' END THEN l.amount::numeric ELSE -l.amount::numeric END),0)::text
 FROM accounts a LEFT JOIN journal_lines l ON l.account_id=a.id
 WHERE a.ledger_id=$1 AND ($2::text='' OR a.id::text=$2)
 GROUP BY a.id ORDER BY a.currency,a.code`, lid, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Balance{}
	for rows.Next() {
		var b Balance
		if err := rows.Scan(&b.AccountID, &b.Code, &b.Kind, &b.Currency, &b.Debits, &b.Credits, &b.Balance); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if id != "" && len(out) == 0 {
		return nil, ErrNotFound
	}
	return out, rows.Err()
}
func (s *Store) Statement(ctx context.Context, lid, id string, limit, offset int) ([]StatementLine, error) {
	var exists bool
	if err := s.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM accounts WHERE ledger_id=$1 AND id=$2)", lid, id).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := s.DB.Query(ctx, "SELECT j.id::text,j.description,j.created_at,l.side,l.amount FROM journal_lines l JOIN journals j ON j.id=l.journal_id WHERE l.ledger_id=$1 AND l.account_id=$2 ORDER BY j.created_at,j.id LIMIT $3 OFFSET $4", lid, id, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StatementLine{}
	for rows.Next() {
		var l StatementLine
		if err := rows.Scan(&l.JournalID, &l.Description, &l.CreatedAt, &l.Side, &l.Amount); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
func (s *Store) Audit(ctx context.Context, lid string, limit, offset int) ([]Audit, error) {
	rows, err := s.DB.Query(ctx, "SELECT id,actor,action,resource_id::text,created_at FROM audit_events WHERE ledger_id=$1 ORDER BY id LIMIT $2 OFFSET $3", lid, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Audit{}
	for rows.Next() {
		var a Audit
		if err := rows.Scan(&a.ID, &a.Actor, &a.Action, &a.ResourceID, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
