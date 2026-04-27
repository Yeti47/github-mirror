package config

import "strings"

// PersonalAccessToken represents a GitHub personal access token.
// It encapsulates the token value and provides safe handling utilities
// so the raw value does not need to be passed around as a plain string.
type PersonalAccessToken struct {
	value string
}

// NewPersonalAccessToken creates a PersonalAccessToken from the given raw value.
func NewPersonalAccessToken(value string) PersonalAccessToken {
	return PersonalAccessToken{value: value}
}

// Value returns the raw token string. Use only where the value must be
// passed to an external system (e.g. git CLI auth header injection).
func (p PersonalAccessToken) Value() string {
	return p.value
}

// RedactAllOccurrencesIn replaces every occurrence of the token in s with
// "[REDACTED]", making the string safe to log or display.
func (p PersonalAccessToken) RedactAllOccurrencesIn(s string) string {
	if p.value == "" {
		return s
	}
	return strings.ReplaceAll(s, p.value, "[REDACTED]")
}
