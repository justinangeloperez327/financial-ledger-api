package ledger

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestAmountPrecision(t *testing.T) {
	for _, raw := range []string{`100`, `"1.1"`, `"01"`, `"+1"`, `"9223372036854775808"`, `null`} {
		var a Amount
		if json.Unmarshal([]byte(raw), &a) == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	var a Amount
	if err := json.Unmarshal([]byte(`"9223372036854775807"`), &a); err != nil || int64(a) != math.MaxInt64 {
		t.Fatal(a, err)
	}
	raw, err := json.Marshal(a)
	if err != nil || string(raw) != `"9223372036854775807"` {
		t.Fatal(string(raw), err)
	}
}
func TestPostingRules(t *testing.T) {
	a, b, c := ID(), ID(), ID()
	valid := Posting{Currency: "AED", Description: "capital", Lines: []Line{{a, "debit", 100}, {b, "credit", 100}}}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := []Posting{
		{"AED", "unbalanced", []Line{{a, "debit", 100}, {b, "credit", 99}}},
		{"AED", "negative", []Line{{a, "debit", -1}, {b, "credit", -1}}},
		{"AED", "same account", []Line{{a, "debit", 100}, {a, "credit", 100}}},
		{"AED", "overflow", []Line{{a, "debit", Amount(math.MaxInt64)}, {b, "debit", 1}, {c, "credit", 1}}},
		{"AED", "one line", []Line{{a, "debit", 1}}},
		{"AED", "invalid side", []Line{{a, "left", 1}, {b, "credit", 1}}},
	}
	for _, p := range cases {
		if !errors.Is(p.Validate(), ErrInvalid) {
			t.Errorf("accepted %s", p.Description)
		}
	}
}
func TestIDs(t *testing.T) {
	if !ValidID(ID()) {
		t.Fatal("generated invalid id")
	}
	for _, id := range []string{"", "../../x", "zzzzzzzz-zzzz-zzzz-zzzz-zzzzzzzzzzzz"} {
		if ValidID(id) {
			t.Fatal(id)
		}
	}
}

func TestTextCharacterLimits(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		max        int
		valid      bool
	}{
		{"Arabic boundary", strings.Repeat("ع", 120), 120, true},
		{"Arabic over boundary", strings.Repeat("ع", 121), 120, false},
		{"Emoji boundary", strings.Repeat("😀", 120), 120, true},
		{"ASCII boundary", strings.Repeat("a", 120), 120, true},
		{"Whitespace", " \t\n", 120, false},
		{"NUL", "name\x00", 120, false},
		{"Invalid UTF8", string([]byte{0xff}), 120, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Text(tc.text, tc.max); got != tc.valid {
				t.Fatalf("Text()=%v, want %v", got, tc.valid)
			}
		})
	}
}
