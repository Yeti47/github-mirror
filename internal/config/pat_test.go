package config_test

import (
	"strings"
	"testing"

	"github.com/Yeti47/github-mirror/internal/config"
)

func TestPersonalAccessToken_RedactAllOccurrencesIn_ReplacesToken(t *testing.T) {
	const token = "ghp_supersecrettoken123"
	pat := config.NewPersonalAccessToken(token)

	input := "error: authentication failed for 'https://github.com/' using token ghp_supersecrettoken123"
	got := pat.RedactAllOccurrencesIn(input)

	if strings.Contains(got, token) {
		t.Errorf("RedactAllOccurrencesIn did not remove the token:\n%s", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("RedactAllOccurrencesIn did not insert [REDACTED] placeholder:\n%s", got)
	}
}

func TestPersonalAccessToken_RedactAllOccurrencesIn_MultipleOccurrences(t *testing.T) {
	const token = "ghp_abc123"
	pat := config.NewPersonalAccessToken(token)

	input := "token=ghp_abc123 and again ghp_abc123"
	got := pat.RedactAllOccurrencesIn(input)

	if strings.Contains(got, token) {
		t.Errorf("RedactAllOccurrencesIn left token in output: %s", got)
	}
	count := strings.Count(got, "[REDACTED]")
	if count != 2 {
		t.Errorf("expected 2 [REDACTED] occurrences, got %d: %s", count, got)
	}
}

func TestPersonalAccessToken_RedactAllOccurrencesIn_EmptyToken(t *testing.T) {
	pat := config.NewPersonalAccessToken("")
	input := "some git output"
	if got := pat.RedactAllOccurrencesIn(input); got != input {
		t.Errorf("RedactAllOccurrencesIn with empty token modified input: %q", got)
	}
}

func TestPersonalAccessToken_RedactAllOccurrencesIn_NoTokenInInput(t *testing.T) {
	pat := config.NewPersonalAccessToken("ghp_secrettoken")
	input := "Cloning into bare repository '/data/owner/repo.git'..."
	if got := pat.RedactAllOccurrencesIn(input); got != input {
		t.Errorf("RedactAllOccurrencesIn modified input that contains no token: %q", got)
	}
}

func TestPersonalAccessToken_Value(t *testing.T) {
	const raw = "ghp_mytoken"
	pat := config.NewPersonalAccessToken(raw)
	if got := pat.Value(); got != raw {
		t.Errorf("Value() = %q, want %q", got, raw)
	}
}
