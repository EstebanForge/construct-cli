// Package env provides environment variable utilities for provider configuration.
package env

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/ui"
)

// ExpandProviderEnv expands ${VAR_NAME} references in provider environment variables
func ExpandProviderEnv(envMap map[string]string) []string {
	var expanded []string
	re := regexp.MustCompile(`\$\{([^}]+)\}`)

	for key, value := range envMap {
		if strings.Contains(value, "${") {
			expandedValue := value
			matches := re.FindAllStringSubmatch(value, -1)

			for _, match := range matches {
				if len(match) > 1 {
					varName := strings.TrimSpace(match[1])
					envValue, exists := os.LookupEnv(varName)

					if !exists {
						ui.LogWarning("Environment variable %s not set for %s (did you forget to export it?)", varName, key)
					}

					expandedValue = strings.ReplaceAll(expandedValue, match[0], envValue)
				}
			}

			expanded = append(expanded, fmt.Sprintf("%s=%s", key, expandedValue))
		} else {
			expanded = append(expanded, fmt.Sprintf("%s=%s", key, value))
		}
	}

	return expanded
}

// MaskSensitiveValue masks sensitive values in debug output
func MaskSensitiveValue(key, value string) string {
	sensitiveKeys := []string{"TOKEN", "KEY", "SECRET", "PASSWORD", "AUTH"}
	for _, sensitive := range sensitiveKeys {
		if strings.Contains(strings.ToUpper(key), sensitive) {
			if len(value) > 10 {
				return value[:4] + "..." + value[len(value)-4:]
			}
			return "***"
		}
	}
	return value
}

// ResetClaudeEnv filters out Claude-related environment variables
// This ensures a clean slate before injecting provider-specific env vars
func ResetClaudeEnv(env []string) []string {
	claudeVars := []string{
		"ANTHROPIC_BASE_URL",
		"ANTHROPIC_AUTH_TOKEN",
		"ANTHROPIC_API_KEY",
		"API_TIMEOUT_MS",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC",
		"ANTHROPIC_CUSTOM_HEADERS",
		"ANTHROPIC_MODEL",
		"ANTHROPIC_SMALL_FAST_MODEL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL",
		"ANTHROPIC_DEFAULT_OPUS_MODEL",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL",
	}

	var filtered []string
	for _, e := range env {
		keep := true
		for _, claudeVar := range claudeVars {
			if strings.HasPrefix(e, claudeVar+"=") {
				keep = false
				break
			}
		}
		if keep {
			filtered = append(filtered, e)
		}
	}

	return filtered
}
