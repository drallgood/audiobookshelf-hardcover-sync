package models

import "errors"

// ErrEditionNotFound marks a lookup that completed successfully but found no edition.
var ErrEditionNotFound = errors.New("edition not found")
