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

func TestSanitizePluralTokensAndLLMCounters(t *testing.T) {
	raw := `{
		"max_tokens": 8192,
		"prompt_tokens": 150,
		"completion_tokens": 300,
		"total_tokens": 450,
		"input_tokens": 120,
		"output_tokens": 240,
		"cache_read_input_tokens": 80,
		"cache_creation_input_tokens": 40,
		"inputTokens": 125,
		"outputTokens": 245,
		"accessTokens": "secret-access-tokens",
		"access_tokens": "secret-access-tokens-snake",
		"api_tokens": "secret-api-tokens",
		"github_tokens": "secret-github-tokens"
	}`
	sanitized, err := sanitizeCredentialData([]byte(raw))
	if err != nil {
		t.Fatalf("sanitizeCredentialData failed: %v", err)
	}
	s := string(sanitized)

	// Counters must be preserved
	if !strings.Contains(s, `"max_tokens": 8192`) {
		t.Errorf("expected max_tokens preserved, got: %s", s)
	}
	if !strings.Contains(s, `"prompt_tokens": 150`) {
		t.Errorf("expected prompt_tokens preserved, got: %s", s)
	}
	if !strings.Contains(s, `"completion_tokens": 300`) {
		t.Errorf("expected completion_tokens preserved, got: %s", s)
	}
	if !strings.Contains(s, `"total_tokens": 450`) {
		t.Errorf("expected total_tokens preserved, got: %s", s)
	}
	if !strings.Contains(s, `"input_tokens": 120`) {
		t.Errorf("expected input_tokens preserved, got: %s", s)
	}
	if !strings.Contains(s, `"output_tokens": 240`) {
		t.Errorf("expected output_tokens preserved, got: %s", s)
	}
	if !strings.Contains(s, `"cache_read_input_tokens": 80`) {
		t.Errorf("expected cache_read_input_tokens preserved, got: %s", s)
	}
	if !strings.Contains(s, `"cache_creation_input_tokens": 40`) {
		t.Errorf("expected cache_creation_input_tokens preserved, got: %s", s)
	}
	if !strings.Contains(s, `"inputTokens": 125`) {
		t.Errorf("expected inputTokens preserved, got: %s", s)
	}
	if !strings.Contains(s, `"outputTokens": 245`) {
		t.Errorf("expected outputTokens preserved, got: %s", s)
	}

	// Plural tokens must be sanitized
	for _, leaked := range []string{
		"secret-access-tokens",
		"secret-access-tokens-snake",
		"secret-api-tokens",
		"secret-github-tokens",
	} {
		if strings.Contains(s, leaked) {
			t.Errorf("plural token key leaked into sanitized output: %q", leaked)
		}
	}
}

func TestIsSensitiveKeyRedactsKeysWithCommonPrefixes(t *testing.T) {
	// Regression: cache_api_key, cache_token, input_api_key, output_secret, cache_authorization
	// must be sanitized as sensitive credentials, even though input_tokens and cache_read_input_tokens are usage counters.
	for _, key := range []string{
		"cache_api_key",
		"cache_token",
		"input_api_key",
		"input_secret",
		"output_secret",
		"cache_authorization",
		"api_key",
		"token",
		"access_token",
	} {
		if !isSensitiveKey(key) {
			t.Errorf("expected %q to be classified as sensitive key", key)
		}
	}

	for _, counterKey := range []string{
		"input_tokens",
		"output_tokens",
		"prompt_tokens",
		"completion_tokens",
		"total_tokens",
		"max_tokens",
		"max_output_tokens",
		"max_completion_tokens",
		"max_input_tokens",
		"reasoning_tokens",
		"input_token",
		"output_token",
		"cache_read_input_tokens",
		"cache_creation_input_tokens",
		"inputTokens",
		"outputTokens",
		"maxOutputTokens",
		"maxCompletionTokens",
	} {
		if isSensitiveKey(counterKey) {
			t.Errorf("expected %q to be classified as non-sensitive counter key", counterKey)
		}
	}
}

func TestSanitizeAndRestoreCredentialRoundTrip(t *testing.T) {
	// 1. Same-key collision avoidance and distinct sentinels
	rawJSON := `{
		"accessToken": "secret-token-one",
		"nested": {
			"accessToken": "secret-token-two"
		},
		"escapedToken": "secret_\"with\"_\\quotes\\",
		"numericToken": 424242,
		"boolSecret": true
	}`

	sanitized, replacements, err := sanitizeCredentialDataWithReplacements([]byte(rawJSON))
	if err != nil {
		t.Fatalf("sanitizeCredentialDataWithReplacements failed: %v", err)
	}

	sanitizedStr := string(sanitized)
	if strings.Contains(sanitizedStr, "secret-token-one") || strings.Contains(sanitizedStr, "secret-token-two") {
		t.Fatalf("secrets leaked into sanitized output: %s", sanitizedStr)
	}
	if strings.Contains(sanitizedStr, "secret_\"with\"_\\quotes\\") {
		t.Fatalf("escaped secret leaked into sanitized output")
	}

	// Verify sentinels are unique per occurrence
	if len(replacements) < 5 {
		t.Fatalf("expected at least 5 replacements, got %d", len(replacements))
	}

	// Restore and verify exact round-trip fidelity
	restored, err := restoreCredentialData(sanitized, replacements)
	if err != nil {
		t.Fatalf("restoreCredentialData failed: %v", err)
	}

	restoredStr := string(restored)
	if !strings.Contains(restoredStr, "secret-token-one") {
		t.Errorf("expected secret-token-one restored: %s", restoredStr)
	}
	if !strings.Contains(restoredStr, "secret-token-two") {
		t.Errorf("expected secret-token-two restored: %s", restoredStr)
	}
	if !strings.Contains(restoredStr, `secret_\"with\"_\\quotes\\`) {
		t.Errorf("expected escaped quotes and backslashes restored: %s", restoredStr)
	}
	if !strings.Contains(restoredStr, `"numericToken": 424242`) {
		t.Errorf("expected numeric value preserved without quotes: %s", restoredStr)
	}
	if !strings.Contains(restoredStr, `"boolSecret": true`) {
		t.Errorf("expected boolean value preserved without quotes: %s", restoredStr)
	}

	// 2. Multi-line JSONL round-trip
	jsonlData := "{\"type\":\"init\",\"accessToken\":\"token-line-1\"}\n{\"type\":\"msg\",\"refreshToken\":\"token-line-2\"}\n"
	sanitizedJSONL, replacementsJSONL, err := sanitizeCredentialDataWithReplacements([]byte(jsonlData))
	if err != nil {
		t.Fatalf("sanitizeCredentialDataWithReplacements jsonl failed: %v", err)
	}
	if strings.Contains(string(sanitizedJSONL), "token-line-1") || strings.Contains(string(sanitizedJSONL), "token-line-2") {
		t.Fatalf("jsonl secrets leaked into sanitized output: %s", string(sanitizedJSONL))
	}

	restoredJSONL, err := restoreCredentialData(sanitizedJSONL, replacementsJSONL)
	if err != nil {
		t.Fatalf("restoreCredentialData jsonl failed: %v", err)
	}
	restoredJSONLStr := string(restoredJSONL)
	if !strings.Contains(restoredJSONLStr, "token-line-1") || !strings.Contains(restoredJSONLStr, "token-line-2") {
		t.Errorf("jsonl tokens not properly restored: %s", restoredJSONLStr)
	}
}
