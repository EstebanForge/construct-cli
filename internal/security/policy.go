package security

import (
	"fmt"
	"net/url"
	"strings"
)

// ProviderID identifies a supported provider.
type ProviderID string

const (
	// ProviderAnthropic identifies Anthropic's Claude API.
	ProviderAnthropic ProviderID = "anthropic"
	// ProviderOpenAI identifies OpenAI's API.
	ProviderOpenAI ProviderID = "openai"
	// ProviderGoogle identifies Google's AI APIs.
	ProviderGoogle ProviderID = "google"
	// ProviderGitHub identifies GitHub's API.
	ProviderGitHub ProviderID = "github"
	// ProviderStripe identifies Stripe's API.
	ProviderStripe ProviderID = "stripe"
	// ProviderSlack identifies Slack's API.
	ProviderSlack ProviderID = "slack"
)

// ProviderPolicy defines security policy for a provider.
type ProviderPolicy struct {
	ID             ProviderID
	AllowedHosts   []string
	AllowedMethods []string
	AuthStrategy   string
	ManagedHeaders []string
	SecretPatterns []string
}

// DefaultProviderRegistry returns the built-in provider policy registry.
func DefaultProviderRegistry() map[ProviderID]*ProviderPolicy {
	return map[ProviderID]*ProviderPolicy{
		ProviderAnthropic: {
			ID:             ProviderAnthropic,
			AllowedHosts:   []string{"api.anthropic.com"},
			AllowedMethods: []string{"POST", "GET"},
			AuthStrategy:   "header",
			ManagedHeaders: []string{"x-api-key", "anthropic-version"},
			SecretPatterns: []string{"sk-ant-*"},
		},
		ProviderOpenAI: {
			ID:             ProviderOpenAI,
			AllowedHosts:   []string{"api.openai.com"},
			AllowedMethods: []string{"POST", "GET"},
			AuthStrategy:   "header",
			ManagedHeaders: []string{"authorization"},
			SecretPatterns: []string{"sk-*"},
		},
		ProviderGoogle: {
			ID:             ProviderGoogle,
			AllowedHosts:   []string{"generativelanguage.googleapis.com", "aiplatform.googleapis.com"},
			AllowedMethods: []string{"POST", "GET"},
			AuthStrategy:   "header",
			ManagedHeaders: []string{"authorization"},
			SecretPatterns: []string{"ya29.*"},
		},
		ProviderGitHub: {
			ID:             ProviderGitHub,
			AllowedHosts:   []string{"api.github.com"},
			AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "PATCH"},
			AuthStrategy:   "header",
			ManagedHeaders: []string{"authorization"},
			SecretPatterns: []string{"ghp_*", "gho_*", "ghu_*"},
		},
		ProviderStripe: {
			ID:             ProviderStripe,
			AllowedHosts:   []string{"api.stripe.com"},
			AllowedMethods: []string{"POST", "GET"},
			AuthStrategy:   "header",
			ManagedHeaders: []string{"authorization"},
			SecretPatterns: []string{"sk_live_*", "sk_test_*"},
		},
		ProviderSlack: {
			ID:             ProviderSlack,
			AllowedHosts:   []string{"slack.com", "api.slack.com"},
			AllowedMethods: []string{"POST", "GET"},
			AuthStrategy:   "header",
			ManagedHeaders: []string{"authorization"},
			SecretPatterns: []string{"xoxb-*", "xoxp-*"},
		},
	}
}

// ProxyPolicy defines proxy-level security controls.
type ProxyPolicy struct {
	ProviderRegistry       map[ProviderID]*ProviderPolicy
	RegistryVersion        string
	RejectAuthHeaders      bool
	RejectRedirects        bool
	SanitizeErrors         bool
	RequireProviderMapping bool
}

// NewProxyPolicy creates a new proxy policy with defaults.
func NewProxyPolicy() *ProxyPolicy {
	return &ProxyPolicy{
		ProviderRegistry:       DefaultProviderRegistry(),
		RegistryVersion:        "1.0.0",
		RejectAuthHeaders:      true,
		RejectRedirects:        true,
		SanitizeErrors:         true,
		RequireProviderMapping: true,
	}
}

// ValidateRequest validates a proxy request against policy.
func (p *ProxyPolicy) ValidateRequest(targetURL string, method string, headers map[string]string) error {
	// Parse URL
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return fmt.Errorf("invalid target URL: %w", err)
	}

	// Check for provider mapping
	provider := p.identifyProvider(parsedURL.Host)
	if provider == nil {
		return fmt.Errorf("unsupported provider host: %s (not in pinning registry)", parsedURL.Host)
	}

	// Validate host
	hostAllowed := false
	for _, allowedHost := range provider.AllowedHosts {
		if parsedURL.Host == allowedHost || strings.HasSuffix(parsedURL.Host, "."+allowedHost) {
			hostAllowed = true
			break
		}
	}
	if !hostAllowed {
		return fmt.Errorf("host not allowed for provider %s: %s", provider.ID, parsedURL.Host)
	}

	// Validate method
	methodAllowed := false
	for _, allowedMethod := range provider.AllowedMethods {
		if method == allowedMethod {
			methodAllowed = true
			break
		}
	}
	if !methodAllowed {
		return fmt.Errorf("method not allowed for provider %s: %s", provider.ID, method)
	}

	// Check for auth-class headers
	if p.RejectAuthHeaders {
		for _, headerName := range []string{"authorization", "proxy-authorization"} {
			if _, exists := headers[strings.ToLower(headerName)]; exists {
				return fmt.Errorf("caller-supplied auth header rejected: %s", headerName)
			}
		}
		// Check provider-managed headers
		for _, managedHeader := range provider.ManagedHeaders {
			if _, exists := headers[strings.ToLower(managedHeader)]; exists {
				return fmt.Errorf("caller-supplied managed header rejected: %s", managedHeader)
			}
		}
	}

	return nil
}

// identifyProvider identifies the provider policy from a hostname.
func (p *ProxyPolicy) identifyProvider(host string) *ProviderPolicy {
	for _, policy := range p.ProviderRegistry {
		for _, allowedHost := range policy.AllowedHosts {
			if host == allowedHost || strings.HasSuffix(host, "."+allowedHost) {
				return policy
			}
		}
	}
	return nil
}

// ProviderForSecret returns the provider ID for a given secret pattern.
func (p *ProxyPolicy) ProviderForSecret(secretValue string) ProviderID {
	for providerID, policy := range p.ProviderRegistry {
		for _, pattern := range policy.SecretPatterns {
			if matchesPattern(secretValue, pattern) {
				return providerID
			}
		}
	}
	return ""
}

// matchesPattern checks if a value matches a simple pattern.
func matchesPattern(value, pattern string) bool {
	// Simple pattern matching (glob-style)
	// For V1, this is basic - production would use more sophisticated matching
	if pattern == "*" {
		return true
	}

	// Handle prefix patterns like "sk-ant-*"
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(value, prefix)
	}

	return value == pattern
}
