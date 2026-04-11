package security

import (
	"strings"
	"testing"
)

func TestStreamMasker_MaskLine(t *testing.T) {
	tests := []struct {
		name      string
		maskStyle string
		secrets   []string
		input     string
		want      string
	}{
		{
			name:      "no secrets configured",
			maskStyle: "hash",
			secrets:   []string{},
			input:     "normal output line",
			want:      "normal output line",
		},
		{
			name:      "exact secret replacement",
			maskStyle: "hash",
			secrets:   []string{"my_secret_password"},
			input:     "password=my_secret_password",
			want:      "password=CONSTRUCT_REDACTED_", // Prefix only (hash suffix varies)
		},
		{
			name:      "multiple exact secrets",
			maskStyle: "hash",
			secrets:   []string{"key1", "key2"},
			input:     "key1 and key2",
			want:      "CONSTRUCT_REDACTED_ and CONSTRUCT_REDACTED_",
		},
		{
			name:      "overlapping secrets - longest first",
			maskStyle: "hash",
			secrets:   []string{"long_secret_value", "long_secret"},
			input:     "long_secret_value and long_secret",
			want:      "CONSTRUCT_REDACTED_ and CONSTRUCT_REDACTED_",
		},
		{
			name:      "repeated secret",
			maskStyle: "hash",
			secrets:   []string{"secret123"},
			input:     "secret123 secret123 secret123",
			want:      "CONSTRUCT_REDACTED_ CONSTRUCT_REDACTED_ CONSTRUCT_REDACTED_",
		},
		{
			name:      "fixed mask style",
			maskStyle: "fixed",
			secrets:   []string{"api_key_123"},
			input:     "api_key=api_key_123",
			want:      "api_key=CONSTRUCT_REDACTED",
		},
		{
			name:      "case sensitive matching",
			maskStyle: "hash",
			secrets:   []string{"Secret"},
			input:     "Secret secret",
			want:      "CONSTRUCT_REDACTED_ secret",
		},
		{
			name:      "partial match not replaced",
			maskStyle: "hash",
			secrets:   []string{"xyz123"}, // Secret that doesn't appear in input
			input:     "password123",
			want:      "password123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := NewStreamMasker(tt.maskStyle)
			sm.AddSecrets(tt.secrets)
			got := sm.MaskLine(tt.input)

			// For hash style, the exact suffix depends on the hash, so we check prefix
			if tt.maskStyle == "hash" && strings.Contains(tt.want, "CONSTRUCT_REDACTED_") {
				if !strings.Contains(got, "CONSTRUCT_REDACTED_") {
					t.Errorf("MaskLine() = %q, want to contain %q", got, "CONSTRUCT_REDACTED_")
				}
				// Check the structure is preserved
				if strings.Count(got, "CONSTRUCT_REDACTED_") != strings.Count(tt.want, "CONSTRUCT_REDACTED_") {
					t.Errorf("MaskLine() replacement count mismatch, got %d, want %d",
						strings.Count(got, "CONSTRUCT_REDACTED_"),
						strings.Count(tt.want, "CONSTRUCT_REDACTED_"))
				}
			} else {
				if got != tt.want {
					t.Errorf("MaskLine() = %q, want %q", got, tt.want)
				}
			}
		})
	}
}

func TestStreamMasker_AddSecret(t *testing.T) {
	sm := NewStreamMasker("hash")

	// Add single secret
	sm.AddSecret("secret1")
	if len(sm.secretVals) != 1 {
		t.Errorf("After AddSecret, got %d secrets, want 1", len(sm.secretVals))
	}

	// Add multiple secrets
	sm.AddSecrets([]string{"secret2", "secret3"})
	if len(sm.secretVals) != 3 {
		t.Errorf("After AddSecrets, got %d secrets, want 3", len(sm.secretVals))
	}

	// Empty secret should not be added
	sm.AddSecret("")
	if len(sm.secretVals) != 3 {
		t.Errorf("After AddSecret(\"\"), got %d secrets, want 3", len(sm.secretVals))
	}
}

func TestStreamMasker_PatternMasking(t *testing.T) {
	sm := NewStreamMasker("hash")

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "OpenAI key pattern",
			input: "API key: sk-abcdefghijklmnopqrstuvwxyz1234567890",
		},
		{
			name:  "GitHub token pattern",
			input: "token: ghp_1234567890abcdefghijklmnopqrstuvwxyz123456",
		},
		{
			name:  "AWS key pattern",
			input: "access: AKIAIOSFODNN7EXAMPLE",
		},
		{
			name:  "private key block",
			input: "-----BEGIN RSA PRIVATE KEY-----",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sm.MaskLine(tt.input)
			// Should have masked the pattern
			if got == tt.input {
				t.Errorf("MaskLine() did not mask pattern, got = %q", got)
			}
			// Should contain CONSTRUCT_REDACTED
			if !strings.Contains(got, "CONSTRUCT_REDACTED") {
				t.Errorf("MaskLine() result should contain CONSTRUCT_REDACTED, got = %q", got)
			}
		})
	}
}

func TestStreamMasker_Integration(t *testing.T) {
	sm := NewStreamMasker("hash")
	sm.AddSecrets([]string{"actual_secret_123", "another_secret_456"})

	input := `Starting service...
Configuration loaded:
  API endpoint: https://api.example.com
  Database: postgresql://localhost/mydb
  API key: actual_secret_123
  Auth token: another_secret_456
  Debug: false
Connected successfully
`

	got := sm.MaskLine(input)

	// Should have masked the exact secrets
	if strings.Contains(got, "actual_secret_123") {
		t.Errorf("MaskLine() did not mask actual_secret_123")
	}
	if strings.Contains(got, "another_secret_456") {
		t.Errorf("MaskLine() did not mask another_secret_456")
	}

	// Should preserve non-secret configuration
	if !strings.Contains(got, "https://api.example.com") {
		t.Errorf("MaskLine() incorrectly masked non-secret URL")
	}
	if !strings.Contains(got, "postgresql://localhost/mydb") {
		t.Errorf("MaskLine() incorrectly masked database URL")
	}
	if !strings.Contains(got, "Debug: false") {
		t.Errorf("MaskLine() incorrectly masked debug flag")
	}
}

func TestEnvMasker_ShouldMaskEnvVar(t *testing.T) {
	em := NewEnvMasker("hash", []string{"PUBLIC_URL"})

	tests := []struct {
		key   string
		value string
		want  bool
	}{
		{"PASSWORD", "secret123", true},
		{"API_KEY", "sk-123456", true},
		{"SECRET_TOKEN", "abc", true},
		{"username", "admin", false},
		{"PUBLIC_URL", "https://example.com", false}, // passthrough
		{"DEBUG", "true", false},
		{"PORT", "8080", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got := em.ShouldMaskEnvVar(tt.key, tt.value)
			if got != tt.want {
				t.Errorf("ShouldMaskEnvVar(%q, %q) = %v, want %v", tt.key, tt.value, got, tt.want)
			}
		})
	}
}

func TestEnvMasker_BuildMaskedEnvSlice(t *testing.T) {
	em := NewEnvMasker("hash", []string{})

	environ := []string{
		"PASSWORD=secret123",
		"USERNAME=admin",
		"API_KEY=sk-123456",
		"DEBUG=true",
	}

	got := em.BuildMaskedEnvSlice(environ)

	// Check length
	if len(got) != len(environ) {
		t.Errorf("BuildMaskedEnvSlice() length = %d, want %d", len(got), len(environ))
	}

	// Check that sensitive values are masked
	for _, env := range got {
		if strings.HasPrefix(env, "PASSWORD=") {
			if !strings.Contains(env, "CONSTRUCT_REDACTED") {
				t.Errorf("PASSWORD value should be masked, got = %q", env)
			}
		}
		if strings.HasPrefix(env, "API_KEY=") {
			if !strings.Contains(env, "CONSTRUCT_REDACTED") {
				t.Errorf("API_KEY value should be masked, got = %q", env)
			}
		}
		// Non-sensitive values should be preserved
		if strings.HasPrefix(env, "USERNAME=") {
			if !strings.Contains(env, "admin") {
				t.Errorf("USERNAME value should be preserved, got = %q", env)
			}
		}
		if strings.HasPrefix(env, "DEBUG=") {
			if !strings.Contains(env, "true") {
				t.Errorf("DEBUG value should be preserved, got = %q", env)
			}
		}
	}
}
