package domain

import (
	"errors"
	"strings"
)

// ErrInvalidURN is returned by ParseURN for malformed input.
var ErrInvalidURN = errors.New("invalid urn")

// URN is LinkedIn's opaque identifier in the form "urn:li:<entity>:<id>".
// The id segment may itself contain colons and parentheses (compound keys,
// nested URNs), so it is taken as everything after "urn:li:<entity>:".
// Treat as opaque outside this package.
type URN string

// ParseURN validates and constructs a URN. The id may contain colons.
func ParseURN(s string) (URN, error) {
	parts := strings.SplitN(s, ":", 4)
	if len(parts) != 4 {
		return "", ErrInvalidURN
	}
	if parts[0] != "urn" || parts[1] != "li" || parts[2] == "" || parts[3] == "" {
		return "", ErrInvalidURN
	}
	return URN(s), nil
}

// Entity returns the entity-type portion (e.g. "job", "fsd_company").
func (u URN) Entity() string {
	parts := strings.SplitN(string(u), ":", 4)
	if len(parts) != 4 {
		return ""
	}
	return parts[2]
}

// ID returns the identifier portion (everything after "urn:li:<entity>:").
func (u URN) ID() string {
	parts := strings.SplitN(string(u), ":", 4)
	if len(parts) != 4 {
		return ""
	}
	return parts[3]
}
