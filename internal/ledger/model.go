package ledger

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalid  = errors.New("invalid request")
	ErrNotFound = errors.New("resource not found")
	ErrConflict = errors.New("operation conflicts with current state")
)

// Amount is an exact minor-unit integer encoded as a JSON string.
type Amount int64

func (a Amount) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(strconv.FormatInt(int64(a), 10))), nil
}
func (a *Amount) UnmarshalJSON(b []byte) error {
	s, err := strconv.Unquote(string(b))
	if err != nil {
		return fmt.Errorf("%w: amounts must be decimal strings", ErrInvalid)
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || strconv.FormatInt(n, 10) != s {
		return fmt.Errorf("%w: invalid minor units", ErrInvalid)
	}
	*a = Amount(n)
	return nil
}
func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
func ValidID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
func Text(s string, max int) bool {
	return strings.TrimSpace(s) != "" && len(s) <= max && !strings.ContainsRune(s, 0)
}

type Ledger struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}
type Account struct {
	ID            string    `json:"id"`
	LedgerID      string    `json:"ledger_id"`
	Code          string    `json:"code"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	Currency      string    `json:"currency"`
	AllowNegative bool      `json:"allow_negative"`
	Closed        bool      `json:"closed"`
	CreatedAt     time.Time `json:"created_at"`
}
type NewAccount struct {
	Code          string `json:"code"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Currency      string `json:"currency"`
	AllowNegative *bool  `json:"allow_negative,omitempty"`
}

func (a NewAccount) Validate() error {
	if !Text(a.Code, 40) || !Text(a.Name, 120) || len(a.Currency) != 3 {
		return ErrInvalid
	}
	switch a.Kind {
	case "asset", "liability", "equity", "income", "expense":
		return nil
	default:
		return ErrInvalid
	}
}

type Line struct {
	AccountID string `json:"account_id"`
	Side      string `json:"side"`
	Amount    Amount `json:"amount"`
}
type Posting struct {
	Currency    string `json:"currency"`
	Description string `json:"description"`
	Lines       []Line `json:"lines"`
}

func (p Posting) Validate() error {
	if len(p.Currency) != 3 || !Text(p.Description, 500) || len(p.Lines) < 2 || len(p.Lines) > 100 {
		return ErrInvalid
	}
	var debit, credit int64
	seen := map[string]bool{}
	for _, l := range p.Lines {
		if !ValidID(l.AccountID) || seen[l.AccountID] || l.Amount <= 0 {
			return ErrInvalid
		}
		seen[l.AccountID] = true
		var total *int64
		switch l.Side {
		case "debit":
			total = &debit
		case "credit":
			total = &credit
		default:
			return ErrInvalid
		}
		if int64(l.Amount) > math.MaxInt64-*total {
			return ErrInvalid
		}
		*total += int64(l.Amount)
	}
	if debit == 0 || debit != credit {
		return fmt.Errorf("%w: debits must equal credits", ErrInvalid)
	}
	return nil
}

type Journal struct {
	ID          string    `json:"id"`
	LedgerID    string    `json:"ledger_id"`
	Currency    string    `json:"currency"`
	Description string    `json:"description"`
	ReversalOf  *string   `json:"reversal_of"`
	Actor       string    `json:"actor"`
	CreatedAt   time.Time `json:"created_at"`
	Lines       []Line    `json:"lines"`
}
type Transfer struct {
	From        string `json:"from_account_id"`
	To          string `json:"to_account_id"`
	Currency    string `json:"currency"`
	Amount      Amount `json:"amount"`
	Description string `json:"description"`
}

func (t Transfer) Posting() Posting {
	return Posting{Currency: t.Currency, Description: t.Description, Lines: []Line{{AccountID: t.From, Side: "credit", Amount: t.Amount}, {AccountID: t.To, Side: "debit", Amount: t.Amount}}}
}

type Balance struct {
	AccountID string `json:"account_id"`
	Code      string `json:"code"`
	Kind      string `json:"kind"`
	Currency  string `json:"currency"`
	Debits    string `json:"debits"`
	Credits   string `json:"credits"`
	Balance   string `json:"balance"`
}
type StatementLine struct {
	JournalID   string    `json:"journal_id"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	Side        string    `json:"side"`
	Amount      Amount    `json:"amount"`
}
type Audit struct {
	ID         int64     `json:"id"`
	Actor      string    `json:"actor"`
	Action     string    `json:"action"`
	ResourceID string    `json:"resource_id"`
	CreatedAt  time.Time `json:"created_at"`
}
