-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION validate_journal(p_id uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE n integer; d numeric; c numeric; invalid_reversal boolean; original_id uuid;
BEGIN
 SELECT reversal_of INTO original_id FROM journals WHERE id=p_id;
 SELECT count(*), coalesce(sum(amount) FILTER(WHERE side='debit'),0),
  coalesce(sum(amount) FILTER(WHERE side='credit'),0) INTO n,d,c FROM journal_lines WHERE journal_id=p_id;
 IF n < 2 OR d=0 OR d<>c OR d>9223372036854775807 THEN
  RAISE EXCEPTION 'journal must balance within int64 limits' USING ERRCODE='23514';
 END IF;
 IF original_id IS NOT NULL THEN
  SELECT EXISTS (
   (SELECT account_id,side,amount FROM journal_lines WHERE journal_id=p_id
    EXCEPT SELECT account_id,CASE side WHEN 'debit' THEN 'credit' ELSE 'debit' END,amount FROM journal_lines WHERE journal_id=original_id)
   UNION ALL
   (SELECT account_id,CASE side WHEN 'debit' THEN 'credit' ELSE 'debit' END,amount FROM journal_lines WHERE journal_id=original_id
    EXCEPT SELECT account_id,side,amount FROM journal_lines WHERE journal_id=p_id)
  ) INTO invalid_reversal;
  IF invalid_reversal THEN RAISE EXCEPTION 'invalid reversal' USING ERRCODE='23514'; END IF;
 END IF;
 IF EXISTS (
  SELECT 1 FROM accounts a JOIN journal_lines touched ON touched.account_id=a.id AND touched.journal_id=p_id
  WHERE NOT a.allow_negative AND (
   SELECT coalesce(sum(CASE WHEN l.side=CASE WHEN a.kind IN ('asset','expense') THEN 'debit' ELSE 'credit' END THEN l.amount::numeric ELSE -l.amount::numeric END),0)
   FROM journal_lines l WHERE l.account_id=a.id
  ) < 0
 ) THEN RAISE EXCEPTION 'negative balance is forbidden' USING ERRCODE='23514'; END IF;
 RETURN;
END;
$$;
CREATE OR REPLACE FUNCTION check_journal() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM validate_journal(NEW.id);
 RETURN NULL;
END;
$$;
CREATE FUNCTION check_journal_line() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM validate_journal(NEW.journal_id);
 RETURN NULL;
END;
$$;
-- Line insertion must queue validation even after a previous SET CONSTRAINTS
-- flushed the journal header event. Immediate constraints validate each statement;
-- the default deferred mode validates the complete journal at transaction end.
CREATE CONSTRAINT TRIGGER balanced_journal_line AFTER INSERT ON journal_lines
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_journal_line();
CREATE FUNCTION guard_account_closure() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT OLD.closed AND NEW.closed AND (
  SELECT coalesce(sum(CASE WHEN side='debit' THEN amount::numeric ELSE -amount::numeric END),0)
  FROM journal_lines WHERE account_id=OLD.id
 ) <> 0 THEN
  RAISE EXCEPTION 'cannot close an account with a nonzero balance' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER account_closure_balance BEFORE UPDATE OF closed ON accounts
 FOR EACH ROW EXECUTE FUNCTION guard_account_closure();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER account_closure_balance ON accounts;
DROP FUNCTION guard_account_closure();
DROP TRIGGER balanced_journal_line ON journal_lines;
DROP FUNCTION check_journal_line();
CREATE OR REPLACE FUNCTION check_journal() RETURNS trigger LANGUAGE plpgsql AS $$
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
DROP FUNCTION validate_journal(uuid);
-- +goose StatementEnd
