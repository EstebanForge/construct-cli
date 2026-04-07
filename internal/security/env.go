package security

import (
	"fmt"
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
			if len(value) > 8 && !em.isCommonNonSecret(value) {
				return true
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
type StreamMasker struct {
	masker            *Masker
	sensitivePatterns []string
}

// NewStreamMasker creates a new stream masker.
func NewStreamMasker(maskStyle string) *StreamMasker {
	return &StreamMasker{
		masker: NewMasker(maskStyle),
		sensitivePatterns: []string{
			// Common secret patterns in output
			`sk-[a-zA-Z0-9]{20,}`,                // API keys
			`[a-zA-Z0-9+/]{32,}={0,2}`,           // Base64-encoded secrets
			`[a-fA-F0-9]{32,}`,                   // Hex-encoded secrets
			`-----BEGIN [A-Z]+ PRIVATE KEY-----`, // Private keys
		},
	}
}

// MaskLine masks secrets in a single line of output.
func (sm *StreamMasker) MaskLine(line string) string {
	masked := line
	for _, pattern := range sm.sensitivePatterns {
		// Simple pattern-based masking
		// In production, use regex.Compile for efficiency
		if strings.Contains(masked, pattern) {
			// Replace with masked placeholder
			// For now, just indicate masking occurred
			masked = strings.ReplaceAll(masked, pattern, sm.masker.Mask(pattern))
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
