-- name: GetCurrency :one
SELECT code, minor_units
FROM currencies
WHERE code = $1;

-- name: ListCurrencies :many
SELECT code, minor_units
FROM currencies
ORDER BY code;
