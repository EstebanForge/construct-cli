package agent

import (
	"strings"
	"testing"
)

func TestSanitizeCredentialData(t *testing.T) {
	rawJSON := `{
  "auth_mode": "chatgpt",
  "tokens": {
    "access_token": "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature_segment_here",
    "refresh_token": "rt.1.real-refresh-token-bytes-here",
    "account_id": "acc-987"
  },
  "api_key": "sk-proj-live-token-12345",
  "non_secret": "keep-me"
}`

	sanitized, err := sanitizeCredentialData([]byte(rawJSON))
	if err != nil {
		t.Fatalf("sanitizeCredentialData failed: %v", err)
	}

	sanitizedStr := string(sanitized)

	// Invariants: real tokens must not be present
	for _, leaked := range []string{
		"signature_segment_here",
		"rt.1.real-refresh-token-bytes-here",
		"sk-proj-live-token-12345",
	} {
		if strings.Contains(sanitizedStr, leaked) {
			t.Errorf("real secret leaked into sanitized output: %s", leaked)
		}
	}

	// Non-secret values must remain intact
	if !strings.Contains(sanitizedStr, `"chatgpt"`) {
		t.Errorf("expected auth_mode chatgpt to be preserved")
	}
	if !strings.Contains(sanitizedStr, `"acc-987"`) {
		t.Errorf("expected account_id acc-987 to be preserved")
	}
	if !strings.Contains(sanitizedStr, `"keep-me"`) {
		t.Errorf("expected non_secret keep-me to be preserved")
	}

	// Sentinels must be substituted
	if !strings.Contains(sanitizedStr, "cnstr_sentinel") {
		t.Errorf("expected sentinel placeholder in sanitized JSON")
	}
}
