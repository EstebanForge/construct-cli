// Package agent manages CLI agent execution, isolation, and configuration synchronization.
package agent

import (
	"encoding/json"
	"strings"
)

// Known JSON credential keys that contain bearer tokens, secrets, or refresh tokens.
var sensitiveCredentialKeys = []string{
	"access_token",
	"access",
	"refresh_token",
	"refresh",
	"token",
	"key",
	"api_key",
	"apikey",
	"secret",
	"id_token",
	"OPENAI_API_KEY",
	"ANTHROPIC_API_KEY",
	"GEMINI_API_KEY",
	"ANTIGRAVITY_API_KEY",
}

// sanitizeCredentialData recursively replaces sensitive token values in JSON credential data
// with sentinel placeholders. Preserves object structure and non-sensitive metadata.
func sanitizeCredentialData(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		// Non-JSON or malformed: return as-is
		return data, nil
	}

	sanitized := sanitizeNode(root)
	return json.MarshalIndent(sanitized, "", "  ")
}

func sanitizeNode(node any) any {
	switch v := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			if isSensitiveKey(k) {
				if s, ok := val.(string); ok && s != "" {
					out[k] = mintCredentialSentinel(k, s)
					continue
				}
			}
			out[k] = sanitizeNode(val)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, elem := range v {
			out[i] = sanitizeNode(elem)
		}
		return out
	default:
		return node
	}
}

func isSensitiveKey(key string) bool {
	for _, sens := range sensitiveCredentialKeys {
		if strings.EqualFold(key, sens) {
			return true
		}
	}
	return false
}

func mintCredentialSentinel(key, val string) string {
	// Preserve JWT shape if original value is a JWT (3 dot-separated segments)
	if strings.Count(val, ".") == 2 {
		return "eyJhbGciOiJub25lIn0.eyJzdWIiOiJjb25zdHJ1Y3Qtc2VudGluZWwiLCJhdWQiOiJjb25zdHJ1Y3QifQ.cnstr_sentinel"
	}

	// Format-mimicking prefixes
	if strings.HasPrefix(val, "sk-ant-oat01-") {
		return "sk-ant-oat01-cnstr_sentinel_placeholder_token_0000000000000000000000000000000000000000"
	}
	if strings.HasPrefix(val, "sk-ant-ort01-") {
		return "sk-ant-ort01-cnstr_sentinel_placeholder_token_0000000000000000000000000000000000000000"
	}
	if strings.HasPrefix(val, "sk-proj-") {
		return "sk-proj-cnstr_sentinel_placeholder_token_00000000000000000000"
	}
	if strings.HasPrefix(val, "ghu_") {
		return "ghu_cnstr_sentinel_placeholder_copilot_token_000000"
	}
	if strings.HasPrefix(val, "ghp_") {
		return "ghp_cnstr_sentinel_placeholder_github_token_000000"
	}

	return "cnstr_sentinel_" + key
}
