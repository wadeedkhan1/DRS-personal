// Package migrations embeds the schema so there is exactly one definition of it.
//
// The schema used to exist twice: as a Go string constant applied at boot, and as
// these .sql files mounted into the Postgres container's init directory. Two copies
// of a schema drift, and the drift only shows up as a confusing runtime error much
// later. Now the files are the definition, the binary carries them, and the same
// migrations run identically for local development and in Docker.
package migrations

import "embed"

// FS holds the forward migrations. Only *.up.sql is embedded: the down files exist
// for a human rolling something back deliberately, and letting the boot path see them
// is how you end up running one by accident.
//
//go:embed *.up.sql
var FS embed.FS
