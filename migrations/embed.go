// Package migrations exposes immutable forward migrations to release binaries.
package migrations

import "embed"

// Files is the migration filesystem consumed by the PostgreSQL migrator.
//
//go:embed *.up.sql
var Files embed.FS
