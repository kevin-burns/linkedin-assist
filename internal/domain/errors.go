package domain

import "errors"

var (
	ErrNotFound  = errors.New("not found")
	ErrAuth      = errors.New("authentication failed")
	ErrSchema    = errors.New("response schema mismatch")
	ErrRateLimit = errors.New("rate limited")
)
