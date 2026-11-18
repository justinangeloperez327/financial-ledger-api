// Package migrations embeds versioned Goose migrations in the migration binary.
package migrations

import "embed"

//go:embed *.sql
var Files embed.FS
