package security

import (
	"fmt"
	"regexp"
	"strings"
)

// EnvMasker creates a masked environment map for agent execution.
type EnvMasker struct {
	masker           *Masker
	passthroughVars  []string
	providerPatterns []SecretPattern
}

// NewEnvMasker creates a new environment masker.
func NewEnvMasker(maskStyle string, passthroughVars []string) *EnvMasker {
	return &EnvMasker{
		masker:           NewMasker(maskStyle),
		passthroughVars:  passthroughVars,
		providerPatterns: DefaultSecretPatterns(),
	}
}

// ShouldMaskEnvVar returns true if an environment variable should be masked.
func (em *EnvMasker) ShouldMaskEnvVar(key, value string) bool {
	// Check passthrough list
	for _, passthrough := range em.passthroughVars {
		if strings.EqualFold(key, passthrough) {
			return false
		}
	}

	// Check if key name suggests it contains sensitive data
	keyLower := strings.ToLower(key)
	sensitiveIndicators := []string{
		"api_key", "apikey", "api-key",
		"secret", "password", "passwd", "pwd",
		"token", "auth", "authorization",
		"dsn", "connection_string", "connection-string",
		"private_key", "private-key", "privatekey",
		"credential", "cert", "certificate",
		"key", "session", "cookie",
	}

	for _, indicator := range sensitiveIndicators {
		if strings.Contains(keyLower, indicator) {
			// Additional check: is value actually sensitive-looking?
			// Skip short values or common non-sensitive values
			// For keys like SECRET_TOKEN, TOKEN, etc., we should mask even short values
			isStrongIndicator := strings.Contains(keyLower, "secret") ||
				strings.Contains(keyLower, "password") ||
				strings.Contains(keyLower, "private_key") ||
				strings.Contains(keyLower, "credential")

			if isStrongIndicator {
				// Mask even short values for strong indicators
				if len(value) > 0 && !em.isCommonNonSecret(value) {
					return true
				}
			} else {
				// For weaker indicators, require longer values
				if len(value) > 8 && !em.isCommonNonSecret(value) {
					return true
				}
			}
		}
	}

	return false
}

// isCommonNonSecret returns true for common non-secret values.
func (em *EnvMasker) isCommonNonSecret(value string) bool {
	commonValues := []string{
		"true", "false", "1", "0", "yes", "no",
		"http", "https", "localhost", "127.0.0.1",
		"0.0.0.0", "development", "production", "staging",
		"test", "testing", "debug", "info", "warn",
		"error", "fatal", "trace",
		"application/json", "text/plain",
		"utf-8", "utf8",
	}

	lowerValue := strings.ToLower(value)
	for _, common := range commonValues {
		if lowerValue == common {
			return true
		}
	}

	return false
}

// MaskEnvValue returns a masked version of an environment variable value.
func (em *EnvMasker) MaskEnvValue(key, value string) string {
	if !em.ShouldMaskEnvVar(key, value) {
		return value
	}
	return em.masker.Mask(value)
}

// BuildMaskedEnvMap creates a masked environment map for agent execution.
func (em *EnvMasker) BuildMaskedEnvMap(environ []string) map[string]string {
	maskedEnv := make(map[string]string)

	for _, envVar := range environ {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := parts[0]
		value := parts[1]

		// Mask the value if needed
		maskedEnv[key] = em.MaskEnvValue(key, value)
	}

	return maskedEnv
}

// BuildMaskedEnvSlice creates a masked environment slice for exec calls.
func (em *EnvMasker) BuildMaskedEnvSlice(environ []string) []string {
	var maskedEnv []string

	for _, envVar := range environ {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) != 2 {
			maskedEnv = append(maskedEnv, envVar)
			continue
		}

		key := parts[0]
		value := parts[1]

		// Mask the value if needed
		maskedValue := em.MaskEnvValue(key, value)
		maskedEnv = append(maskedEnv, fmt.Sprintf("%s=%s", key, maskedValue))
	}

	return maskedEnv
}

// StreamMasker masks secrets in stdout/stderr streams in real-time.
// It tracks known secret values and uses longest-match-first replacement.
type StreamMasker struct {
	masker     *Masker
	secretVals []string // Known secret values for exact matching
	patterns   []*regexp.Regexp // Compiled patterns for detection
}

// NewStreamMasker creates a new stream masker.
func NewStreamMasker(maskStyle string) *StreamMasker {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`sk-[a-zA-Z0-9]{20,}`),                 // API keys
		regexp.MustCompile(`ghp_[a-zA-Z0-9]{36}`),                 // GitHub tokens
		regexp.MustCompile(`sk-ant-[a-zA-Z0-9\-]{95}`),            // Anthropic keys
		regexp.MustCompile(`AKIA[0-9A-Z]{16}`),                    // AWS keys
		regexp.MustCompile(`[a-zA-Z0-9+/]{32,}={0,2}`),            // Base64-encoded secrets
		regexp.MustCompile(`-----BEGIN [A-Z]+ PRIVATE KEY-----`),  // Private key blocks
	}

	return &StreamMasker{
		masker:     NewMasker(maskStyle),
		secretVals: []string{},
		patterns:   patterns,
	}
}

// AddSecret adds a secret value to the masker for exact matching.
func (sm *StreamMasker) AddSecret(secret string) {
	if secret != "" {
		sm.secretVals = append(sm.secretVals, secret)
	}
}

// AddSecrets adds multiple secret values to the masker.
func (sm *StreamMasker) AddSecrets(secrets []string) {
	for _, secret := range secrets {
		sm.AddSecret(secret)
	}
}

// MaskLine masks secrets in a single line of output.
// It performs exact matching first (for known secrets), then pattern-based masking.
func (sm *StreamMasker) MaskLine(line string) string {
	masked := line

	// First, do exact value replacement for known secrets
	// Sort by length (longest first) to handle overlaps
	for i := 0; i < len(sm.secretVals); i++ {
		for j := i + 1; j < len(sm.secretVals); j++ {
			if len(sm.secretVals[i]) < len(sm.secretVals[j]) {
				sm.secretVals[i], sm.secretVals[j] = sm.secretVals[j], sm.secretVals[i]
			}
		}
	}

	// Replace exact secret values
	for _, secret := range sm.secretVals {
		if strings.Contains(masked, secret) {
			masked = strings.ReplaceAll(masked, secret, sm.masker.Mask(secret))
		}
	}

	// Then, do pattern-based masking for things we can't know exactly
	for _, pattern := range sm.patterns {
		if pattern.MatchString(masked) {
			// Replace matches with masked placeholder
			masked = pattern.ReplaceAllString(masked, sm.masker.Mask(pattern.String()))
		}
	}

	return masked
}

// DetectSecretsInLine returns true if a line contains potential secrets.
func (sm *StreamMasker) DetectSecretsInLine(line string) bool {
	lowerLine := strings.ToLower(line)
	sensitiveKeywords := []string{
		"api_key", "apikey", "api-key",
		"secret", "password", "passwd", "pwd",
		"token", "auth_token", "authorization",
		"private_key", "private-key",
		"credential",
	}

	for _, keyword := range sensitiveKeywords {
		if strings.Contains(lowerLine, keyword) {
			// Check if there's a value that looks like a secret
			if strings.Contains(line, "=") || strings.Contains(line, ":") {
				return true
			}
		}
	}

	// Check for patterns
	if strings.Contains(line, "sk-") || strings.Contains(line, "BEGIN PRIVATE KEY") {
		return true
	}

	return false
}
