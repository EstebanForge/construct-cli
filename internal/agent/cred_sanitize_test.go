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

func TestSanitizeCredentialDataCamelCaseAndFailClosed(t *testing.T) {
	camelJSON := `{
  "claudeAiOauth": {
    "accessToken": "sk-ant-oat01-live-access-token",
    "refreshToken": "sk-ant-ort01-live-refresh-token"
  },
  "apiKey": "sk-proj-test-12345"
}`
	sanitized, err := sanitizeCredentialData([]byte(camelJSON))
	if err != nil {
		t.Fatalf("sanitizeCredentialData failed: %v", err)
	}
	s := string(sanitized)
	for _, leaked := range []string{"live-access-token", "live-refresh-token", "sk-proj-test-12345"} {
		if strings.Contains(s, leaked) {
			t.Errorf("leaked secret: %s", leaked)
		}
	}

	// Malformed JSON must fail closed (return error)
	_, err = sanitizeCredentialData([]byte("not valid json {"))
	if err == nil {
		t.Fatal("expected error on malformed json, got nil")
	}
}

func TestSanitizeCredentialDataArrayHeadersAndPrecision(t *testing.T) {
	complexJSON := `{
  "userId": 9223372036854775807,
  "tokens": ["secret-token-1", "secret-token-2"],
  "password": "super-secret-password",
  "authorization": "Bearer real-bearer-token",
  "x-api-key": "header-secret-key",
  "numericToken": 987654321
}`

	sanitized, err := sanitizeCredentialData([]byte(complexJSON))
	if err != nil {
		t.Fatalf("sanitizeCredentialData failed: %v", err)
	}

	s := string(sanitized)
	for _, leaked := range []string{
		"secret-token-1",
		"secret-token-2",
		"super-secret-password",
		"real-bearer-token",
		"header-secret-key",
	} {
		if strings.Contains(s, leaked) {
			t.Errorf("leaked secret %q in sanitized output", leaked)
		}
	}

	// Precision of large integer must be preserved exactly
	if !strings.Contains(s, "9223372036854775807") {
		t.Errorf("large integer precision was lost: %s", s)
	}
}

func TestSanitizePrecisionKeybindingsAndAuthTokens(t *testing.T) {
	raw := `{"max_tokens":4096,"key":"ctrl+x","cookie":"sid=abc","jwt":"eyJ.x.y","sessionKey":"s3cr3t","token":"real-token-value"}`
	sanitized, err := sanitizeCredentialData([]byte(raw))
	if err != nil {
		t.Fatalf("sanitizeCredentialData failed: %v", err)
	}
	s := string(sanitized)

	// max_tokens and key (keybinding) must be preserved
	if !strings.Contains(s, `"max_tokens": 4096`) {
		t.Errorf("expected max_tokens 4096 to be preserved, got: %s", s)
	}
	if !strings.Contains(s, `"key": "ctrl+x"`) {
		t.Errorf("expected key ctrl+x to be preserved, got: %s", s)
	}

	// cookie, jwt, sessionKey, token must be sanitized
	for _, leaked := range []string{"sid=abc", "eyJ.x.y", "s3cr3t", "real-token-value"} {
		if strings.Contains(s, leaked) {
			t.Errorf("leaked secret %q in sanitized output: %s", leaked, s)
		}
	}
}
