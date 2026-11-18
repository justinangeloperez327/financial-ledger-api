package auth

import (
	"strings"
	"testing"
	"time"
)

func TestVerification(t *testing.T) {
	now := time.Unix(1800000000, 0)
	v := Verifier{[]byte(strings.Repeat("x", 32)), "issuer", "audience"}
	c := Claims{Subject: "operator", Issuer: v.Issuer, Audience: v.Audience, IssuedAt: now.Unix(), NotBefore: now.Unix(), Expires: now.Add(time.Hour).Unix(), Ledgers: map[string]string{"one": "writer", "two": "reader"}}
	token, err := v.Sign(c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := v.Verify(token, now)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Can("one", true, false) || got.Can("one", true, true) || got.Can("two", true, false) || got.Can("other", false, false) {
		t.Fatal("role boundary broken")
	}
	if _, err = v.Verify(token, now.Add(time.Hour)); err == nil {
		t.Fatal("accepted expired token")
	}
	for _, change := range []func(*Claims){func(c *Claims) { c.Issuer = "other" }, func(c *Claims) { c.Audience = "other" }, func(c *Claims) { c.Subject = "" }, func(c *Claims) { c.IssuedAt = now.Unix() + 1 }, func(c *Claims) { c.NotBefore = now.Unix() + 1 }, func(c *Claims) { c.Expires = now.Add(2 * time.Hour).Unix() }, func(c *Claims) { c.Ledgers = map[string]string{"one": "owner"} }} {
		bad := c
		change(&bad)
		s, _ := v.Sign(bad)
		if _, err := v.Verify(s, now); err == nil {
			t.Fatal("accepted invalid claims")
		}
	}
	for _, bad := range []string{"", token + "x", "eyJhbGciOiJub25lIn0.e30.", "a.b.c"} {
		if _, err := v.Verify(bad, now); err == nil {
			t.Fatal("accepted invalid signature")
		}
	}
	other := v
	other.Secret = []byte(strings.Repeat("y", 32))
	if _, err := other.Verify(token, now); err == nil {
		t.Fatal("accepted wrong key")
	}
}
