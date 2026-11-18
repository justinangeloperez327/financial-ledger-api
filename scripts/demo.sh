#!/usr/bin/env bash
set -euo pipefail
: "${TOKEN:?Export a global operator TOKEN first}"
API_URL=${API_URL:-http://localhost:8080}
command -v jq >/dev/null
request() {
  local method=$1 path=$2 body=$3 key=${4:-}
  curl --fail-with-body --silent --show-error -X "$method" "$API_URL$path" \
    -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
    -H "Idempotency-Key: $key" -d "$body"
}
ledger=$(request POST /v1/ledgers '{"name":"Portfolio demo"}' | jq -er .id)
base="/v1/ledgers/$ledger"
cash=$(request POST "$base/accounts" '{"code":"1000","name":"Cash","kind":"asset","currency":"AED","allow_negative":false}' | jq -er .id)
bank=$(request POST "$base/accounts" '{"code":"1100","name":"Bank","kind":"asset","currency":"AED","allow_negative":false}' | jq -er .id)
equity=$(request POST "$base/accounts" '{"code":"3000","name":"Capital","kind":"equity","currency":"AED"}' | jq -er .id)
opening=$(jq -nc --arg cash "$cash" --arg equity "$equity" '{currency:"AED",description:"Opening capital",lines:[{account_id:$cash,side:"debit",amount:"100000"},{account_id:$equity,side:"credit",amount:"100000"}]}')
journal=$(request POST "$base/journals" "$opening" opening-capital | jq -er .id)
retry=$(request POST "$base/journals" "$opening" opening-capital | jq -er .id)
test "$journal" = "$retry"
transfer=$(jq -nc --arg from "$cash" --arg to "$bank" '{from_account_id:$from,to_account_id:$to,currency:"AED",amount:"25000",description:"Cash to bank"}')
transfer_id=$(request POST "$base/transfers" "$transfer" bank-transfer | jq -er .id)
request POST "$base/journals/$transfer_id/reverse" '{"description":"Reverse demonstration transfer"}' reverse-transfer | jq .
request GET "$base/trial-balance" '{}' | jq .
request GET "$base/audit-events" '{}' | jq .
printf 'Demo passed. Ledger: %s; matching retry journal: %s\n' "$ledger" "$journal"
