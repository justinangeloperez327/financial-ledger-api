// Package auth verifies short-lived HS256 access tokens issued by a trusted operator.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type Claims struct {
	Subject   string            `json:"sub"`
	Issuer    string            `json:"iss"`
	Audience  string            `json:"aud"`
	Expires   int64             `json:"exp"`
	IssuedAt  int64             `json:"iat"`
	NotBefore int64             `json:"nbf"`
	Admin     bool              `json:"admin,omitempty"`
	Ledgers   map[string]string `json:"ledgers,omitempty"`
}
type Verifier struct {
	Secret   []byte
	Issuer   string
	Audience string
}

var ErrToken = errors.New("invalid access token")

func (v Verifier) Verify(token string, now time.Time) (Claims, error) {
	var c Claims
	if len(v.Secret) < 32 || v.Issuer == "" || v.Audience == "" || len(token) > 16384 {
		return c, ErrToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return c, ErrToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return c, ErrToken
	}
	var h struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	if json.Unmarshal(raw, &h) != nil || h.Alg != "HS256" || h.Typ != "JWT" {
		return c, ErrToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return c, ErrToken
	}
	mac := hmac.New(sha256.New, v.Secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return c, ErrToken
	}
	raw, err = base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || json.Unmarshal(raw, &c) != nil {
		return Claims{}, ErrToken
	}
	n := now.Unix()
	if strings.TrimSpace(c.Subject) == "" || len(c.Subject) > 200 || c.Issuer != v.Issuer || c.Audience != v.Audience || c.Expires <= n || c.IssuedAt <= 0 || c.IssuedAt > n || c.NotBefore > n || c.Expires <= c.IssuedAt || c.Expires-c.IssuedAt > 3600 {
		return Claims{}, ErrToken
	}
	for _, role := range c.Ledgers {
		if role != "reader" && role != "writer" && role != "admin" {
			return Claims{}, ErrToken
		}
	}
	return c, nil
}
func (c Claims) Can(id string, write, admin bool) bool {
	if c.Admin {
		return true
	}
	role := c.Ledgers[id]
	if admin {
		return role == "admin"
	}
	if write {
		return role == "admin" || role == "writer"
	}
	return role == "reader" || role == "writer" || role == "admin"
}
func (v Verifier) Sign(c Claims) (string, error) {
	if len(v.Secret) < 32 {
		return "", ErrToken
	}
	h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	body := h + "." + base64.RawURLEncoding.EncodeToString(b)
	mac := hmac.New(sha256.New, v.Secret)
	mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
