package env

import (
	"os"
	"strings"
	"testing"
)

// TestExpandProviderEnv tests environment variable expansion
func TestExpandProviderEnv(t *testing.T) {
	// Set test environment
	os.Setenv("TEST_API_URL", "https://test.example.com")
	os.Setenv("TEST_API_KEY", "sk-test-12345")
	defer os.Unsetenv("TEST_API_URL")
	defer os.Unsetenv("TEST_API_KEY")

	envMap := map[string]string{
		"ANTHROPIC_BASE_URL":   "${TEST_API_URL}",
		"ANTHROPIC_AUTH_TOKEN": "${TEST_API_KEY}",
		"API_TIMEOUT_MS":       "3000000",
		"MIXED_VALUE":          "prefix-${TEST_API_KEY}-suffix",
	}

	expanded := ExpandProviderEnv(envMap)

	// Build expected map
	expectedMap := map[string]string{
		"ANTHROPIC_BASE_URL":   "https://test.example.com",
		"ANTHROPIC_AUTH_TOKEN": "sk-test-12345",
		"API_TIMEOUT_MS":       "3000000",
		"MIXED_VALUE":          "prefix-sk-test-12345-suffix",
	}

	// Convert slice to map for easier comparison
	resultMap := make(map[string]string)
	for _, env := range expanded {
		parts := splitEnvString(env)
		if len(parts) != 2 {
			t.Errorf("Invalid env format: %s", env)
			continue
		}
		resultMap[parts[0]] = parts[1]
	}

	// Check all expected values
	for key, expectedValue := range expectedMap {
		actualValue, exists := resultMap[key]
		if !exists {
			t.Errorf("Expected key %s not found in result", key)
			continue
		}
		if actualValue != expectedValue {
			t.Errorf("For %s: expected %s, got %s", key, expectedValue, actualValue)
		}
	}
}

// TestExpandProviderEnvMissing tests missing environment variable handling
func TestExpandProviderEnvMissing(t *testing.T) {
	// Ensure env var doesn't exist
	os.Unsetenv("NONEXISTENT_VAR")

	envMap := map[string]string{
		"TEST_VAR": "${NONEXISTENT_VAR}",
	}

	expanded := ExpandProviderEnv(envMap)

	if len(expanded) != 1 {
		t.Fatalf("Expected 1 env var, got %d", len(expanded))
	}

	parts := splitEnvString(expanded[0])
	if len(parts) != 2 {
		t.Fatalf("Invalid env format: %s", expanded[0])
	}

	// Should be empty string when var doesn't exist
	if parts[1] != "" {
		t.Errorf("Expected empty string for missing var, got %s", parts[1])
	}
}

func TestExpandProviderEnvFallbackCNSTR(t *testing.T) {
	os.Unsetenv("CNSTR_ZAI_API_KEY")
	os.Setenv("ZAI_API_KEY", "sk-zai-123")
	defer os.Unsetenv("ZAI_API_KEY")

	envMap := map[string]string{
		"ANTHROPIC_AUTH_TOKEN": "${CNSTR_ZAI_API_KEY}",
	}

	expanded := ExpandProviderEnv(envMap)

	if len(expanded) != 1 {
		t.Fatalf("Expected 1 env var, got %d", len(expanded))
	}

	parts := splitEnvString(expanded[0])
	if len(parts) != 2 {
		t.Fatalf("Invalid env format: %s", expanded[0])
	}

	if parts[1] != "sk-zai-123" {
		t.Errorf("Expected fallback value sk-zai-123, got %s", parts[1])
	}
}

func TestExpandProviderEnvPrefersCNSTR(t *testing.T) {
	os.Setenv("CNSTR_ZAI_API_KEY", "sk-cnstr-456")
	os.Setenv("ZAI_API_KEY", "sk-zai-123")
	defer os.Unsetenv("CNSTR_ZAI_API_KEY")
	defer os.Unsetenv("ZAI_API_KEY")

	envMap := map[string]string{
		"ANTHROPIC_AUTH_TOKEN": "${CNSTR_ZAI_API_KEY}",
	}

	expanded := ExpandProviderEnv(envMap)

	if len(expanded) != 1 {
		t.Fatalf("Expected 1 env var, got %d", len(expanded))
	}

	parts := splitEnvString(expanded[0])
	if len(parts) != 2 {
		t.Fatalf("Invalid env format: %s", expanded[0])
	}

	if parts[1] != "sk-cnstr-456" {
		t.Errorf("Expected CNSTR value sk-cnstr-456, got %s", parts[1])
	}
}

func TestExpandProviderEnvFallbackWhenCNSTREmpty(t *testing.T) {
	os.Setenv("CNSTR_ZAI_API_KEY", "")
	os.Setenv("ZAI_API_KEY", "sk-zai-123")
	defer os.Unsetenv("CNSTR_ZAI_API_KEY")
	defer os.Unsetenv("ZAI_API_KEY")

	envMap := map[string]string{
		"ANTHROPIC_AUTH_TOKEN": "${CNSTR_ZAI_API_KEY}",
	}

	expanded := ExpandProviderEnv(envMap)

	if len(expanded) != 1 {
		t.Fatalf("Expected 1 env var, got %d", len(expanded))
	}

	parts := splitEnvString(expanded[0])
	if len(parts) != 2 {
		t.Fatalf("Invalid env format: %s", expanded[0])
	}

	if parts[1] != "sk-zai-123" {
		t.Errorf("Expected fallback value sk-zai-123, got %s", parts[1])
	}
}

func TestCollectProviderEnvFallbackWhenCNSTREmpty(t *testing.T) {
	os.Setenv("CNSTR_OPENAI_API_KEY", "")
	os.Setenv("OPENAI_API_KEY", "sk-openai-123")
	defer os.Unsetenv("CNSTR_OPENAI_API_KEY")
	defer os.Unsetenv("OPENAI_API_KEY")

	envs := CollectProviderEnv()

	values := make(map[string]string, len(envs))
	for _, e := range envs {
		parts := splitEnvString(e)
		if len(parts) != 2 {
			t.Fatalf("Invalid env format: %s", e)
		}
		values[parts[0]] = parts[1]
	}

	if values["OPENAI_API_KEY"] != "sk-openai-123" {
		t.Errorf("Expected OPENAI_API_KEY to be set, got %s", values["OPENAI_API_KEY"])
	}
	if values["CNSTR_OPENAI_API_KEY"] != "sk-openai-123" {
		t.Errorf("Expected CNSTR_OPENAI_API_KEY fallback to be set, got %s", values["CNSTR_OPENAI_API_KEY"])
	}
}

func TestMergeEnvVarsOverride(t *testing.T) {
	base := []string{"OPENAI_API_KEY=base", "HF_TOKEN=base-hf"}
	override := []string{"OPENAI_API_KEY=override", "GEMINI_API_KEY=gem"}

	merged := MergeEnvVars(base, override)
	values := make(map[string]string, len(merged))
	for _, e := range merged {
		parts := splitEnvString(e)
		if len(parts) != 2 {
			t.Fatalf("Invalid env format: %s", e)
		}
		values[parts[0]] = parts[1]
	}

	if values["OPENAI_API_KEY"] != "override" {
		t.Errorf("Expected override value for OPENAI_API_KEY, got %s", values["OPENAI_API_KEY"])
	}
	if values["HF_TOKEN"] != "base-hf" {
		t.Errorf("Expected base HF_TOKEN to be preserved, got %s", values["HF_TOKEN"])
	}
	if values["GEMINI_API_KEY"] != "gem" {
		t.Errorf("Expected GEMINI_API_KEY to be set, got %s", values["GEMINI_API_KEY"])
	}
}

// TestMaskSensitiveValue tests sensitive value masking
func TestMaskSensitiveValue(t *testing.T) {
	tests := []struct {
		key      string
		value    string
		expected string
	}{
		{"ANTHROPIC_AUTH_TOKEN", "sk-ant-1234567890abcdef", "sk-a...cdef"},
		{"API_KEY", "short", "***"},
		{"REGULAR_VAR", "some-value", "some-value"},
		{"PASSWORD", "my-secret-password", "my-s...word"},
		{"TIMEOUT", "3000000", "3000000"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			result := MaskSensitiveValue(tt.key, tt.value)
			if result != tt.expected {
				t.Errorf("For %s: expected %s, got %s", tt.key, tt.expected, result)
			}
		})
	}
}

// TestResetClaudeEnv tests Claude environment variable filtering
func TestResetClaudeEnv(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"HOME=/home/user",
		"ANTHROPIC_BASE_URL=https://old.example.com",
		"ANTHROPIC_AUTH_TOKEN=old-token",
		"ANTHROPIC_MODEL=old-model",
		"API_TIMEOUT_MS=999",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=0",
		"REGULAR_VAR=keep-this",
		"ANTHROPIC_API_KEY=old-key",
	}

	filtered := ResetClaudeEnv(env)

	// Expected: Only non-Claude vars remain
	expected := map[string]bool{
		"PATH=/usr/bin":         true,
		"HOME=/home/user":       true,
		"REGULAR_VAR=keep-this": true,
	}

	if len(filtered) != len(expected) {
		t.Errorf("Expected %d env vars after reset, got %d", len(expected), len(filtered))
	}

	for _, e := range filtered {
		if !expected[e] {
			t.Errorf("Unexpected env var after reset: %s", e)
		}
	}

	// Ensure Claude vars were filtered
	for _, e := range filtered {
		if strings.HasPrefix(e, "ANTHROPIC_") || strings.HasPrefix(e, "API_TIMEOUT_MS=") ||
			strings.HasPrefix(e, "CLAUDE_CODE_") {
			t.Errorf("Claude env var not filtered: %s", e)
		}
	}
}

// Helper function to split "KEY=VALUE" into [KEY, VALUE]
func splitEnvString(env string) []string {
	for i := 0; i < len(env); i++ {
		if env[i] == '=' {
			return []string{env[:i], env[i+1:]}
		}
	}
	return []string{env}
}
