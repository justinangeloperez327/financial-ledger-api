-- +goose Up
CREATE TABLE ledgers (
 id uuid PRIMARY KEY, name text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE accounts (
 id uuid PRIMARY KEY, ledger_id uuid NOT NULL REFERENCES ledgers(id),
 code text NOT NULL CHECK (length(code) BETWEEN 1 AND 40),
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
 kind text NOT NULL CHECK (kind IN ('asset','liability','equity','income','expense')),
 currency text NOT NULL REFERENCES currencies(code), allow_negative boolean NOT NULL DEFAULT true,
 closed boolean NOT NULL DEFAULT false, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (ledger_id,code), UNIQUE (ledger_id,id,currency)
);
CREATE TABLE journals (
 id uuid PRIMARY KEY, ledger_id uuid NOT NULL REFERENCES ledgers(id),
 currency text NOT NULL REFERENCES currencies(code),
 description text NOT NULL CHECK (length(description) BETWEEN 1 AND 500),
 reversal_of uuid UNIQUE, actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 created_xid xid8 NOT NULL DEFAULT pg_current_xact_id(), UNIQUE (ledger_id,id,currency),
 FOREIGN KEY (ledger_id,reversal_of,currency) REFERENCES journals(ledger_id,id,currency)
);
CREATE TABLE journal_lines (
 journal_id uuid NOT NULL, ledger_id uuid NOT NULL, account_id uuid NOT NULL, currency text NOT NULL,
 side text NOT NULL CHECK (side IN ('debit','credit')), amount bigint NOT NULL CHECK (amount > 0),
 PRIMARY KEY (journal_id,account_id),
 FOREIGN KEY (ledger_id,journal_id,currency) REFERENCES journals(ledger_id,id,currency),
 FOREIGN KEY (ledger_id,account_id,currency) REFERENCES accounts(ledger_id,id,currency)
);
CREATE INDEX journal_lines_account ON journal_lines(ledger_id,account_id,journal_id);
CREATE INDEX journals_ledger_order ON journals(ledger_id,created_at,id);
CREATE TABLE idempotency_keys (
 ledger_id uuid NOT NULL REFERENCES ledgers(id), key text NOT NULL CHECK (length(key) BETWEEN 1 AND 128),
 fingerprint text NOT NULL, journal_id uuid NOT NULL REFERENCES journals(id), PRIMARY KEY (ledger_id,key)
);
CREATE TABLE audit_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, ledger_id uuid NOT NULL REFERENCES ledgers(id),
 actor text NOT NULL, action text NOT NULL, resource_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
-- +goose StatementBegin
CREATE FUNCTION forbid_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'financial history is immutable' USING ERRCODE='23514'; END;
$$;
CREATE TRIGGER journals_immutable BEFORE UPDATE OR DELETE ON journals FOR EACH ROW EXECUTE FUNCTION forbid_mutation();
CREATE TRIGGER lines_immutable BEFORE UPDATE OR DELETE ON journal_lines FOR EACH ROW EXECUTE FUNCTION forbid_mutation();
CREATE TRIGGER audit_immutable BEFORE UPDATE OR DELETE ON audit_events FOR EACH ROW EXECUTE FUNCTION forbid_mutation();
CREATE FUNCTION guard_line() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE a accounts; x xid8;
BEGIN
 SELECT created_xid INTO x FROM journals WHERE id=NEW.journal_id;
 IF x IS DISTINCT FROM pg_current_xact_id() THEN
  RAISE EXCEPTION 'cannot append to a committed journal' USING ERRCODE='23514';
 END IF;
 SELECT * INTO a FROM accounts WHERE id=NEW.account_id FOR UPDATE;
 IF a.closed THEN RAISE EXCEPTION 'account is closed' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER line_guard BEFORE INSERT ON journal_lines FOR EACH ROW EXECUTE FUNCTION guard_line();
CREATE FUNCTION check_journal() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE n integer; d numeric; c numeric; invalid_reversal boolean;
BEGIN
 SELECT count(*), coalesce(sum(amount) FILTER(WHERE side='debit'),0),
  coalesce(sum(amount) FILTER(WHERE side='credit'),0) INTO n,d,c FROM journal_lines WHERE journal_id=NEW.id;
 IF n < 2 OR d=0 OR d<>c OR d>9223372036854775807 THEN
  RAISE EXCEPTION 'journal must balance within int64 limits' USING ERRCODE='23514';
 END IF;
 IF NEW.reversal_of IS NOT NULL THEN
  SELECT EXISTS (
   (SELECT account_id,side,amount FROM journal_lines WHERE journal_id=NEW.id
    EXCEPT SELECT account_id,CASE side WHEN 'debit' THEN 'credit' ELSE 'debit' END,amount FROM journal_lines WHERE journal_id=NEW.reversal_of)
   UNION ALL
   (SELECT account_id,CASE side WHEN 'debit' THEN 'credit' ELSE 'debit' END,amount FROM journal_lines WHERE journal_id=NEW.reversal_of
    EXCEPT SELECT account_id,side,amount FROM journal_lines WHERE journal_id=NEW.id)
  ) INTO invalid_reversal;
  IF invalid_reversal THEN RAISE EXCEPTION 'invalid reversal' USING ERRCODE='23514'; END IF;
 END IF;
 IF EXISTS (
  SELECT 1 FROM accounts a JOIN journal_lines touched ON touched.account_id=a.id AND touched.journal_id=NEW.id
  WHERE NOT a.allow_negative AND (
   SELECT coalesce(sum(CASE WHEN l.side=CASE WHEN a.kind IN ('asset','expense') THEN 'debit' ELSE 'credit' END THEN l.amount::numeric ELSE -l.amount::numeric END),0)
   FROM journal_lines l WHERE l.account_id=a.id
  ) < 0
 ) THEN RAISE EXCEPTION 'negative balance is forbidden' USING ERRCODE='23514'; END IF;
 RETURN NULL;
END;
$$;
CREATE CONSTRAINT TRIGGER balanced_journal AFTER INSERT ON journals DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_journal();
CREATE FUNCTION guard_account() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR (NEW.id,NEW.ledger_id,NEW.code,NEW.name,NEW.kind,NEW.currency,NEW.allow_negative,NEW.created_at)
  IS DISTINCT FROM (OLD.id,OLD.ledger_id,OLD.code,OLD.name,OLD.kind,OLD.currency,OLD.allow_negative,OLD.created_at) THEN
  RAISE EXCEPTION 'account identity and policy are immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER account_guard BEFORE UPDATE OR DELETE ON accounts FOR EACH ROW EXECUTE FUNCTION guard_account();
-- +goose StatementEnd

-- +goose Down
DROP TABLE audit_events, idempotency_keys, journal_lines, journals, accounts, ledgers CASCADE;
DROP FUNCTION guard_account(), check_journal(), guard_line(), forbid_mutation();
