//go:build tools

package internal

// This file is used to ensure the tools package is included in the go.mod file.
// It should not contain any actual code.

import (
	_ "github.com/rs/zerolog"
)
