// Package agent manages CLI agent execution, isolation, and configuration synchronization.
package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Known JSON credential keys that contain bearer tokens, secrets, or refresh tokens.
var sensitiveCredentialKeys = []string{
	"access_token",
	"accessToken",
	"access",
	"refresh_token",
	"refreshToken",
	"refresh",
	"token",
	"api_key",
	"apiKey",
	"apikey",
	"access_key",
	"accessKey",
	"secret_key",
	"secretKey",
	"private_key",
	"privateKey",
	"session_key",
	"sessionKey",
	"cookie",
	"cookies",
	"jwt",
	"secret",
	"id_token",
	"idToken",
	"claudeAiOauth",
	"claude_ai_oauth",
	"oauth_token",
	"oauthToken",
	"session_token",
	"sessionToken",
	"client_secret",
	"clientSecret",
	"OPENAI_API_KEY",
	"ANTHROPIC_API_KEY",
	"GEMINI_API_KEY",
	"ANTIGRAVITY_API_KEY",
}

// sanitizeCredentialData recursively replaces sensitive token values in JSON credential data
// with sentinel placeholders. Preserves object structure and non-sensitive metadata.
func sanitizeCredentialData(data []byte) ([]byte, error) {
	sanitized, _, err := sanitizeCredentialDataWithReplacements(data)
	return sanitized, err
}

func sanitizeCredentialDataWithReplacements(data []byte) ([]byte, map[string]string, error) {
	if len(data) == 0 {
		return data, nil, nil
	}

	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var root any
	if err := d.Decode(&root); err != nil {
		return nil, nil, fmt.Errorf("malformed credential json: %w", err)
	}

	replacements := make(map[string]string)
	sanitized := sanitizeNodeWithReplacements(root, replacements)
	out, err := json.MarshalIndent(sanitized, "", "  ")
	return out, replacements, err
}

func sanitizeNodeWithReplacements(node any, replacements map[string]string) any {
	switch v := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			if isSensitiveKey(k) {
				switch vt := val.(type) {
				case string:
					if vt != "" {
						s := mintCredentialSentinel(k, vt)
						out[k] = s
						replacements[s] = vt
						continue
					}
				case []any:
					arr := make([]any, len(vt))
					for i, elem := range vt {
						if es, ok := elem.(string); ok && es != "" {
							s := mintCredentialSentinel(fmt.Sprintf("%s_%d", k, i), es)
							arr[i] = s
							replacements[s] = es
						} else {
							arr[i] = sanitizeNodeWithReplacements(elem, replacements)
						}
					}
					out[k] = arr
					continue
				case map[string]any:
					out[k] = sanitizeNodeWithReplacements(vt, replacements)
					continue
				default:
					if vt != nil {
						orig := fmt.Sprintf("%v", vt)
						s := mintCredentialSentinel(k, orig)
						out[k] = s
						replacements[s] = orig
						continue
					}
				}
			}
			out[k] = sanitizeNodeWithReplacements(val, replacements)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, elem := range v {
			out[i] = sanitizeNodeWithReplacements(elem, replacements)
		}
		return out
	default:
		return node
	}
}

func isSensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	if strings.HasPrefix(lower, "non_") ||
		strings.HasPrefix(lower, "max_") ||
		strings.HasPrefix(lower, "num_") ||
		strings.HasPrefix(lower, "total_") ||
		strings.HasPrefix(lower, "prompt_") ||
		strings.HasPrefix(lower, "completion_") ||
		strings.HasSuffix(lower, "_mode") ||
		strings.HasSuffix(lower, "_type") ||
		strings.HasSuffix(lower, "_id") ||
		strings.HasSuffix(lower, "_url") ||
		strings.HasSuffix(lower, "_path") ||
		strings.HasSuffix(lower, "_file") ||
		strings.HasSuffix(lower, "_count") ||
		strings.HasSuffix(lower, "_limit") ||
		strings.HasSuffix(lower, "_size") ||
		strings.HasSuffix(lower, "_length") ||
		lower == "max_tokens" {
		return false
	}
	for _, sens := range sensitiveCredentialKeys {
		if strings.EqualFold(key, sens) {
			return true
		}
	}
	clean := strings.ReplaceAll(strings.ReplaceAll(lower, "_", ""), "-", "")
	return strings.HasSuffix(clean, "token") ||
		strings.HasSuffix(clean, "tokens") ||
		strings.HasSuffix(clean, "secret") ||
		strings.HasSuffix(clean, "secrets") ||
		strings.HasSuffix(clean, "password") ||
		strings.HasSuffix(clean, "apikey") ||
		strings.HasSuffix(clean, "sessionkey") ||
		strings.HasSuffix(clean, "cookie") ||
		strings.HasSuffix(clean, "cookies") ||
		clean == "jwt" ||
		clean == "auth" ||
		clean == "authorization" ||
		clean == "credential" ||
		clean == "credentials" ||
		strings.HasSuffix(clean, "accesskey") ||
		strings.HasSuffix(clean, "privatekey") ||
		clean == "bearer"
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
