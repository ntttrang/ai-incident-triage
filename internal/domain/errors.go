package domain

import "errors"

// Sentinel errors shared across layers. Adapters map them to HTTP statuses;
// services return them from use cases.
var (
	ErrNotFound      = errors.New("not found")
	ErrConflict      = errors.New("conflict")
	ErrInvalidInput  = errors.New("invalid input")
)
