package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// ConfinementStatus represents the result of the post-boot network confinement probe.
type ConfinementStatus string

// Confinement status values.
const (
	ConfinementOK         ConfinementStatus = "ok"
	ConfinementUnenforced ConfinementStatus = "unenforced"
)

// ProbeProxyConfinement tests from the host whether the proxy is responding.
// Fails closed if the proxy cannot be contacted within timeout.
func ProbeProxyConfinement(ctx context.Context, proxyURL string) (ConfinementStatus, error) {
	if proxyURL == "" {
		return ConfinementUnenforced, fmt.Errorf("proxy URL is empty")
	}

	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return ConfinementUnenforced, fmt.Errorf("invalid proxy URL %q: %w", proxyURL, err)
	}

	client := &http.Client{
		Timeout: 3 * time.Second,
	}

	// Probe proxy port directly
	probeURL := fmt.Sprintf("http://%s/", parsed.Host)
	req, err := http.NewRequestWithContext(ctx, "GET", probeURL, nil)
	if err != nil {
		return ConfinementUnenforced, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return ConfinementUnenforced, fmt.Errorf("proxy probe failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // probe response cleanup
	}()

	// The proxy requires authentication, so 407 (or 200/403/404 depending on auth) indicates it is answering.
	if resp.StatusCode == http.StatusProxyAuthRequired || resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusForbidden {
		return ConfinementOK, nil
	}

	return ConfinementOK, nil
}
