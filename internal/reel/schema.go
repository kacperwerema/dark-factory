package reel

import _ "embed"

// Schema is the storyboard's JSON Schema (draft 2020-12), for editors and
// other tools; Problems is the full check.
//
//go:embed schema.json
var Schema []byte
