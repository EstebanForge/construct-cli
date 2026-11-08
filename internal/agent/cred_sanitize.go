// Package agent manages CLI agent execution, isolation, and configuration synchronization.
package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

// sanitizeCredentialData recursively replaces sensitive token values in JSON/JSONL credential data
// with sentinel placeholders. Preserves object structure and non-sensitive metadata.
func sanitizeCredentialData(data []byte) ([]byte, error) {
	sanitized, _, err := sanitizeCredentialDataWithReplacements(data)
	return sanitized, err
}

func sanitizeCredentialDataWithReplacements(data []byte) ([]byte, map[string]any, error) {
	if len(data) == 0 {
		return data, nil, nil
	}

	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var root any
	if err := d.Decode(&root); err == nil {
		var next any
		if d.Decode(&next) == io.EOF {
			replacements := make(map[string]any)
			sanitized := sanitizeNodeWithReplacements(root, replacements)
			out, err := json.MarshalIndent(sanitized, "", "  ")
			return out, replacements, err
		}
	}

	return sanitizeJSONLines(data)
}

func sanitizeJSONLines(data []byte) ([]byte, map[string]any, error) {
	lines := bytes.Split(data, []byte("\n"))
	var out [][]byte
	replacements := make(map[string]any)
	for _, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			out = append(out, line)
			continue
		}
		var root any
		d := json.NewDecoder(bytes.NewReader(line))
		d.UseNumber()
		if err := d.Decode(&root); err != nil {
			return nil, nil, fmt.Errorf("malformed credential jsonl line: %w", err)
		}
		sanitized := sanitizeNodeWithReplacements(root, replacements)
		b, err := json.Marshal(sanitized)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, b)
	}
	return bytes.Join(out, []byte("\n")), replacements, nil
}

// restoreCredentialData walks the parsed JSON/JSONL structure and restores original values
// for round-tripped sentinels at the AST level, ensuring proper string escaping and type preservation.
func restoreCredentialData(data []byte, replacements map[string]any) ([]byte, error) {
	if len(data) == 0 || len(replacements) == 0 {
		return data, nil
	}

	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var root any
	if err := d.Decode(&root); err == nil {
		var next any
		if d.Decode(&next) == io.EOF {
			restored := restoreNode(root, replacements)
			return json.MarshalIndent(restored, "", "  ")
		}
	}

	return restoreJSONLines(data, replacements)
}

func restoreJSONLines(data []byte, replacements map[string]any) ([]byte, error) {
	lines := bytes.Split(data, []byte("\n"))
	var out [][]byte
	for _, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			out = append(out, line)
			continue
		}
		var root any
		d := json.NewDecoder(bytes.NewReader(line))
		d.UseNumber()
		if err := d.Decode(&root); err != nil {
			return nil, fmt.Errorf("malformed credential jsonl line: %w", err)
		}
		restored := restoreNode(root, replacements)
		b, err := json.Marshal(restored)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return bytes.Join(out, []byte("\n")), nil
}

func restoreNode(node any, replacements map[string]any) any {
	switch v := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			out[k] = restoreNode(val, replacements)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, elem := range v {
			out[i] = restoreNode(elem, replacements)
		}
		return out
	case string:
		if orig, ok := replacements[v]; ok {
			return orig
		}
		return v
	default:
		return node
	}
}

func sanitizeNodeWithReplacements(node any, replacements map[string]any) any {
	switch v := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			if isSensitiveKey(k) {
				switch vt := val.(type) {
				case string:
					if vt != "" {
						s := mintCredentialSentinel(k, vt, len(replacements)+1)
						out[k] = s
						replacements[s] = vt
						continue
					}
				case []any:
					arr := make([]any, len(vt))
					for i, elem := range vt {
						if es, ok := elem.(string); ok && es != "" {
							s := mintCredentialSentinel(fmt.Sprintf("%s_%d", k, i), es, len(replacements)+1)
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
						origStr := fmt.Sprintf("%v", vt)
						s := mintCredentialSentinel(k, origStr, len(replacements)+1)
						out[k] = s
						replacements[s] = vt
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
		strings.HasPrefix(lower, "input_") ||
		strings.HasPrefix(lower, "output_") ||
		strings.HasPrefix(lower, "cache_") ||
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
		lower == "max_tokens" ||
		lower == "maxtokens" ||
		lower == "inputtokens" ||
		lower == "outputtokens" ||
		lower == "prompttokens" ||
		lower == "completiontokens" ||
		lower == "totaltokens" ||
		strings.HasPrefix(lower, "cacheread") ||
		strings.HasPrefix(lower, "cachecreation") {
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

func mintCredentialSentinel(key, val string, id int) string {
	cleanKey := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
	if cleanKey == "" {
		cleanKey = "token"
	}

	// Preserve JWT shape if original value is a JWT (3 dot-separated segments)
	if strings.Count(val, ".") == 2 {
		return fmt.Sprintf("eyJhbGciOiJub25lIn0.eyJzdWIiOiJjb25zdHJ1Y3Qtc2VudGluZWwiLCJhdWQiOiJjb25zdHJ1Y3QifQ.cnstr_sentinel_%d", id)
	}

	// Format-mimicking prefixes with unique index suffix
	if strings.HasPrefix(val, "sk-ant-oat01-") {
		return fmt.Sprintf("sk-ant-oat01-cnstr_sentinel_placeholder_token_%016x", id)
	}
	if strings.HasPrefix(val, "sk-ant-ort01-") {
		return fmt.Sprintf("sk-ant-ort01-cnstr_sentinel_placeholder_token_%016x", id)
	}
	if strings.HasPrefix(val, "sk-proj-") {
		return fmt.Sprintf("sk-proj-cnstr_sentinel_placeholder_token_%016x", id)
	}
	if strings.HasPrefix(val, "ghu_") {
		return fmt.Sprintf("ghu_cnstr_sentinel_placeholder_copilot_token_%08x", id)
	}
	if strings.HasPrefix(val, "ghp_") {
		return fmt.Sprintf("ghp_cnstr_sentinel_placeholder_github_token_%08x", id)
	}

	return fmt.Sprintf("cnstr_sentinel_%s_%d", cleanKey, id)
}
