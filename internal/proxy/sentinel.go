package proxy

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
)

// SecretBinding tracks the relationship between environment variable, real secret, sentinel, and bound host.
type SecretBinding struct {
	VarName   string
	RealValue string
	Sentinel  string
	BoundHost string
}

// SecretRegistry manages sentinel placeholders and token swapping for the session proxy.
type SecretRegistry struct {
	mu          sync.RWMutex
	bindings    []SecretBinding
	bySentinel  map[string]SecretBinding
	realSecrets [][]byte
}

// NewSecretRegistry initializes an empty secret registry.
func NewSecretRegistry() *SecretRegistry {
	return &SecretRegistry{
		bySentinel: make(map[string]SecretBinding),
	}
}

// Register adds a secret, mints a format-mimicking sentinel placeholder, and records the host binding.
func (r *SecretRegistry) Register(varName, realValue, boundHost string) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	boundHost = strings.ToLower(strings.TrimSpace(boundHost))

	// If realValue is already a minted sentinel, do not double-sentinelize.
	if _, ok := r.bySentinel[realValue]; ok {
		return realValue
	}

	// If already registered for this variable and value, return existing sentinel.
	for _, b := range r.bindings {
		if b.VarName == varName && b.RealValue == realValue && b.BoundHost == boundHost {
			return b.Sentinel
		}
	}

	sentinel := mintPlaceholder(varName)

	b := SecretBinding{
		VarName:   varName,
		RealValue: realValue,
		Sentinel:  sentinel,
		BoundHost: boundHost,
	}

	r.bindings = append(r.bindings, b)
	r.bySentinel[sentinel] = b
	if len(realValue) >= 8 {
		r.realSecrets = append(r.realSecrets, []byte(realValue))
	}

	return sentinel
}

// IsSentinel reports whether val is a registered sentinel placeholder.
func (r *SecretRegistry) IsSentinel(val string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.bySentinel[val]
	return ok
}

// LookupReal returns the real secret for a sentinel if targetHost matches the secret's bound host.
func (r *SecretRegistry) LookupReal(sentinel, targetHost string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	b, ok := r.bySentinel[sentinel]
	if !ok {
		return "", false
	}

	if matchHostScope(b.BoundHost, targetHost) {
		return b.RealValue, true
	}

	return "", false
}

// ContainsRealSecret checks whether any registered real secret appears verbatim in data.
func (r *SecretRegistry) ContainsRealSecret(data []byte) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, secret := range r.realSecrets {
		if bytes.Contains(data, secret) {
			return true
		}
	}
	return false
}

// SwapHeaders replaces sentinels with real secrets in request headers for authorized destination hosts.
// Fails closed if real secret bytes are detected prior to swapping (outbound leak guard).
func (r *SecretRegistry) SwapHeaders(h http.Header, targetHost string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	targetHost = strings.ToLower(strings.TrimSpace(targetHost))

	for key, values := range h {
		for i, val := range values {
			// 1. Leak scanner: drop request if unswapped real secrets exist in raw headers
			if r.ContainsRealSecret([]byte(val)) {
				return fmt.Errorf("outbound leak detected in header %q: contains real secret", key)
			}

			// 2. Direct sentinel substitution
			for sentinel, binding := range r.bySentinel {
				if strings.Contains(val, sentinel) {
					if matchHostScope(binding.BoundHost, targetHost) {
						val = strings.ReplaceAll(val, sentinel, binding.RealValue)
					}
				}

				// 3. Scan base64 Basic-auth values
				if strings.HasPrefix(strings.ToLower(val), "basic ") {
					encoded := strings.TrimSpace(val[6:])
					if decoded, err := base64.StdEncoding.DecodeString(encoded); err == nil {
						decodedStr := string(decoded)
						if strings.Contains(decodedStr, sentinel) {
							if matchHostScope(binding.BoundHost, targetHost) {
								replaced := strings.ReplaceAll(decodedStr, sentinel, binding.RealValue)
								val = "Basic " + base64.StdEncoding.EncodeToString([]byte(replaced))
							}
						}
					}
				}
			}
			values[i] = val
		}
	}

	return nil
}

// DefaultHostBindings returns the default API endpoint for recognized provider variables.
func DefaultHostBindings(varName string) string {
	switch varName {
	case "ANTHROPIC_API_KEY":
		return "api.anthropic.com"
	case "OPENAI_API_KEY":
		return "api.openai.com"
	case "ANTIGRAVITY_API_KEY":
		return "generativelanguage.googleapis.com"
	case "OPENROUTER_API_KEY":
		return "openrouter.ai"
	case "HF_TOKEN":
		return "huggingface.co"
	default:
		return ""
	}
}

func matchHostScope(boundHost, targetHost string) bool {
	if boundHost == "" {
		return false
	}
	boundHost = strings.ToLower(boundHost)
	targetHost = strings.ToLower(targetHost)

	if targetHost == boundHost {
		return true
	}
	if strings.HasSuffix(targetHost, "."+boundHost) {
		return true
	}
	return false
}

func mintPlaceholder(varName string) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	randStr := func(n int) string {
		out := make([]byte, n)
		for i := 0; i < n; i++ {
			num, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
			if err != nil {
				out[i] = 'x'
				continue
			}
			out[i] = charset[num.Int64()]
		}
		return string(out)
	}

	switch varName {
	case "ANTHROPIC_API_KEY":
		return "sk-ant-api03-" + randStr(80)
	case "OPENAI_API_KEY":
		return "sk-proj-" + randStr(48)
	case "ANTIGRAVITY_API_KEY":
		return "AIzaSy" + randStr(33)
	case "OPENROUTER_API_KEY":
		return "sk-or-v1-" + randStr(64)
	case "HF_TOKEN":
		return "hf_" + randStr(34)
	default:
		return "sk-cnstr-sentinel-" + randStr(32)
	}
}
