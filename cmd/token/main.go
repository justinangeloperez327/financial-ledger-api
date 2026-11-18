// token is an offline operator tool, never an unauthenticated HTTP endpoint.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/justinangeloperez327/financial-ledger-api/internal/auth"
	"github.com/justinangeloperez327/financial-ledger-api/internal/ledger"
)

func main() {
	subject := flag.String("subject", "local-operator", "audit actor")
	lid := flag.String("ledger", "", "ledger UUID")
	role := flag.String("role", "reader", "reader, writer or admin")
	admin := flag.Bool("admin", false, "global operator access; bootstrap only")
	flag.Parse()
	if !*admin && (!ledger.ValidID(*lid) || (*role != "reader" && *role != "writer" && *role != "admin")) {
		fmt.Fprintln(os.Stderr, "provide -ledger UUID and -role reader|writer|admin, or -admin")
		os.Exit(1)
	}
	issuer := os.Getenv("JWT_ISSUER")
	if issuer == "" {
		issuer = "financial-ledger-api"
	}
	audience := os.Getenv("JWT_AUDIENCE")
	if audience == "" {
		audience = "financial-ledger-api"
	}
	v := auth.Verifier{Secret: []byte(os.Getenv("JWT_SECRET")), Issuer: issuer, Audience: audience}
	now := time.Now()
	c := auth.Claims{Subject: *subject, Issuer: issuer, Audience: audience, IssuedAt: now.Unix(), NotBefore: now.Unix(), Expires: now.Add(time.Hour).Unix(), Admin: *admin}
	if *lid != "" {
		c.Ledgers = map[string]string{*lid: *role}
	}
	token, err := v.Sign(c)
	if err != nil {
		fmt.Fprintln(os.Stderr, "JWT_SECRET must contain at least 32 bytes")
		os.Exit(1)
	}
	if _, err = v.Verify(token, now); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(token)
}
