-- +goose Up
CREATE TABLE currencies (
    code TEXT PRIMARY KEY CHECK (code ~ '^[A-Z]{3}$'),
    minor_units SMALLINT NOT NULL CHECK (minor_units BETWEEN 0 AND 4),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO currencies (code, minor_units) VALUES
    ('AED', 2),
    ('PHP', 2),
    ('USD', 2);

-- +goose Down
DROP TABLE IF EXISTS currencies;
