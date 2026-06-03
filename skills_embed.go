package main

import "embed"

// skillEmbedFS holds the bundled SKILL.md and future skill files for
// `dbgov install <agent> --skills`.
//
//go:embed skills/dbgov-cli
var skillEmbedFS embed.FS
