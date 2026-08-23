// Package migrations is the embedded .sql files and nothing else.
//
// IT IS A PACKAGE RATHER THAN A DIRECTORY internal/postgres READS, because
// //go:embed cannot reach outside the package's own directory: `//go:embed
// ../../migrations` does not compile. The alternative was to move the .sql
// files under internal/postgres, and the layout in CLAUDE.md puts them at the
// repository root where a reviewer looking for the schema will find them.
//
// BOTH .up.sql AND .down.sql ARE EMBEDDED. The runner applies only the up
// files, and refuses an up file with no down file beside it — which it can
// only check if the down files are here.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
