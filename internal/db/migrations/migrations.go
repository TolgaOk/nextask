// Package migrations embeds SQL migration files.
package migrations

import "embed"

// FS provides access to embedded SQL migration files.
//
//go:embed *.sql
var FS embed.FS
