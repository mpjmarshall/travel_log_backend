// Package migrations is the embedded *.sql files and nothing else.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
