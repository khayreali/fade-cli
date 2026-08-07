// Package data holds the seed catalog compiled into the binary. Keeping the
// JSON at the repo root (rather than inside internal/catalog) means expanding
// to a new neighborhood is a data edit, not a code edit.
package data

import _ "embed"

//go:embed shops.json
var SeedJSON []byte
