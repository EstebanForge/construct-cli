// Package env provides environment variable utilities for provider configuration.
package env

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/ui"
)

// PathComponents defines the standard PATH components for The Construct environment.
// Single source of truth for PATH generation (see scripts/generate-paths.go).
var PathComponents = []string{
	"/home/linuxbrew/.linuxbrew/bin",
	"/home/linuxbrew/.linuxbrew/sbin",
	"$HOME/.local/bin",
	"$HOME/.npm-global/bin",
	"$HOME/.cargo/bin",
	"$HOME/.bun/bin",
	"$HOME/.asdf/bin",
	"$HOME/.asdf/shims",
	"$HOME/.local/share/mise/bin",
	"$HOME/.local/share/mise/shims",
	"$HOME/.volta/bin",
	"$HOME/.local/share/pnpm",
	"$HOME/.yarn/bin",
	"$HOME/.config/yarn/global/node_modules/.bin",
	"$HOME/go/bin",
	"$HOME/.composer/vendor/bin",
	"$HOME/.config/composer/vendor/bin",
	"$HOME/.nix-profile/bin",
	"/nix/var/nix/profiles/default/bin",
	"$HOME/.phpbrew/bin",
	"/usr/local/sbin",
	"/usr/local/bin",
	"/usr/sbin",
	"/usr/bin",
	"/sbin",
	"/bin",
}

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
					envValue, exists := lookupEnvWithFallback(varName)

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

// lookupEnvWithFallback allows CNSTR_ prefixed variables to fall back to unprefixed names.
func lookupEnvWithFallback(varName string) (string, bool) {
	if value, exists := os.LookupEnv(varName); exists && value != "" {
		return value, true
	}

	if strings.HasPrefix(varName, "CNSTR_") {
		if value, exists := os.LookupEnv(strings.TrimPrefix(varName, "CNSTR_")); exists && value != "" {
			if ui.CurrentLogLevel >= ui.LogLevelDebug {
				fmt.Printf("Debug: Using fallback env var %s for %s\n", strings.TrimPrefix(varName, "CNSTR_"), varName)
			}
			return value, true
		}
	}

	return "", false
}

// ProviderKeyVars defines unprefixed provider key names supported for passthrough and aliasing.
var ProviderKeyVars = []string{
	"ANTHROPIC_API_KEY",
	"OPENAI_API_KEY",
	"GEMINI_API_KEY",
	"OPENROUTER_API_KEY",
	"ZAI_API_KEY",
	"OPENCODE_API_KEY",
	"HF_TOKEN",
	"KIMI_API_KEY",
	"MINIMAX_API_KEY",
	"MINIMAX_CN_API_KEY",
}

// CollectProviderEnv builds environment variables for common provider keys.
// It passes through unprefixed keys and ensures CNSTR_ aliases exist when only unprefixed keys are set.
func CollectProviderEnv() []string {
	envs := make([]string, 0, len(ProviderKeyVars)*2)
	added := make(map[string]struct{}, len(ProviderKeyVars)*2)

	add := func(key, value string) {
		if _, exists := added[key]; exists {
			return
		}
		envs = append(envs, fmt.Sprintf("%s=%s", key, value))
		added[key] = struct{}{}
	}

	for _, key := range ProviderKeyVars {
		cnstrKey := "CNSTR_" + key
		if value, exists := os.LookupEnv(cnstrKey); exists && value != "" {
			add(cnstrKey, value)
		}

		if value, exists := os.LookupEnv(key); exists && value != "" {
			add(key, value)
			if cnstrValue, exists := os.LookupEnv(cnstrKey); !exists || cnstrValue == "" {
				add(cnstrKey, value)
			}
		}
	}

	return envs
}

// MergeEnvVars merges base and override env vars, keeping override values on key conflicts.
func MergeEnvVars(base []string, override []string) []string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}

	orderedKeys := make([]string, 0, len(base)+len(override))
	values := make(map[string]string, len(base)+len(override))

	addKey := func(key, value string) {
		if _, exists := values[key]; !exists {
			orderedKeys = append(orderedKeys, key)
		}
		values[key] = value
	}

	for _, entry := range base {
		key, value := splitEnvEntry(entry)
		if key == "" {
			continue
		}
		addKey(key, value)
	}

	for _, entry := range override {
		key, value := splitEnvEntry(entry)
		if key == "" {
			continue
		}
		addKey(key, value)
	}

	merged := make([]string, 0, len(values))
	for _, key := range orderedKeys {
		merged = append(merged, fmt.Sprintf("%s=%s", key, values[key]))
	}

	return merged
}

func splitEnvEntry(entry string) (string, string) {
	for i := 0; i < len(entry); i++ {
		if entry[i] == '=' {
			return entry[:i], entry[i+1:]
		}
	}
	return "", ""
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

// BuildConstructPath builds the comprehensive PATH for The Construct environment
// Returns a PATH string with all standard components, expanding $HOME references
func BuildConstructPath(homeDir string) string {
	paths := make([]string, 0, len(PathComponents))
	for _, component := range PathComponents {
		// Expand $HOME references
		expanded := strings.ReplaceAll(component, "$HOME", homeDir)
		paths = append(paths, expanded)
	}
	return strings.Join(paths, ":")
}

// EnsureConstructPath ensures the environment has the full Construct PATH set
// If PATH exists in env, it prepends the Construct paths. Otherwise, it sets a new PATH.
func EnsureConstructPath(env *[]string, homeDir string) {
	constructPath := BuildConstructPath(homeDir)

	if ui.CurrentLogLevel >= ui.LogLevelDebug {
		fmt.Printf("Debug: Built PATH: %s\n", constructPath[:150]+"...")
	}

	// Find existing PATH in environment
	pathFound := false
	for i, e := range *env {
		if strings.HasPrefix(e, "PATH=") {
			// Preserve any existing paths from the original environment
			existingPath := strings.TrimPrefix(e, "PATH=")

			// Combine: Construct paths first, then existing paths
			// This ensures our paths take priority
			if existingPath != "" {
				(*env)[i] = "PATH=" + constructPath + ":" + existingPath
			} else {
				(*env)[i] = "PATH=" + constructPath
			}
			pathFound = true
			break
		}
	}

	// If no PATH found, add it
	if !pathFound {
		*env = append(*env, "PATH="+constructPath)
	}
}
