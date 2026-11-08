package agent

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
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

// ProbeProxyConfinement tests from the host whether the proxy is responding and enforcing authentication.
// Fails closed if the proxy cannot be contacted or fails authentication within timeout.
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

	probeHost := parsed.Host
	if h, port, err := net.SplitHostPort(parsed.Host); err == nil {
		if h == "host.docker.internal" || h == "host.microsandbox.internal" {
			probeHost = net.JoinHostPort("127.0.0.1", port)
			if bridgeIP := linuxBridgeIP(); bridgeIP != "" {
				if conn, err := net.DialTimeout("tcp", probeHost, 200*time.Millisecond); err == nil {
					_ = conn.Close() //nolint:errcheck
				} else {
					probeHost = net.JoinHostPort(bridgeIP, port)
				}
			}
		}
	} else if parsed.Host == "host.docker.internal" || parsed.Host == "host.microsandbox.internal" {
		probeHost = "127.0.0.1"
	}

	// 1. Unauthenticated probe: proxy MUST demand authentication (407 Proxy Authentication Required)
	probeURL := fmt.Sprintf("http://%s/", probeHost)
	reqUnauth, err := http.NewRequestWithContext(ctx, "GET", probeURL, nil)
	if err != nil {
		return ConfinementUnenforced, err
	}

	respUnauth, err := client.Do(reqUnauth)
	if err != nil {
		return ConfinementUnenforced, fmt.Errorf("proxy probe failed: %w", err)
	}
	defer func() {
		_ = respUnauth.Body.Close() //nolint:errcheck // probe response cleanup
	}()

	if respUnauth.StatusCode != http.StatusProxyAuthRequired {
		return ConfinementUnenforced, fmt.Errorf("expected 407 Proxy Authentication Required on unauthenticated probe, got %d", respUnauth.StatusCode)
	}

	// 2. Negative auth probe: verify proxy rejects incorrect session token with 407
	reqBadAuth, err := http.NewRequestWithContext(ctx, "GET", probeURL, nil)
	if err != nil {
		return ConfinementUnenforced, err
	}
	badToken := base64.StdEncoding.EncodeToString([]byte("construct:bogus_invalid_password_xyz"))
	reqBadAuth.Header.Set("Proxy-Authorization", "Basic "+badToken)
	respBadAuth, bErr := client.Do(reqBadAuth)
	if bErr != nil {
		return ConfinementUnenforced, fmt.Errorf("negative proxy auth probe failed: %w", bErr)
	}
	defer func() {
		_ = respBadAuth.Body.Close() //nolint:errcheck // probe response cleanup
	}()
	if respBadAuth.StatusCode != http.StatusProxyAuthRequired {
		return ConfinementUnenforced, fmt.Errorf("proxy accepted invalid session token (expected 407, got %d)", respBadAuth.StatusCode)
	}

	// 3. Authenticated probe: verify proxy authenticates session token
	if parsed.User != nil {
		reqAuth, err := http.NewRequestWithContext(ctx, "GET", probeURL, nil)
		if err != nil {
			return ConfinementUnenforced, err
		}
		password, _ := parsed.User.Password()
		token := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", parsed.User.Username(), password)))
		reqAuth.Header.Set("Proxy-Authorization", "Basic "+token)

		respAuth, err := client.Do(reqAuth)
		if err != nil {
			return ConfinementUnenforced, fmt.Errorf("authenticated proxy probe failed: %w", err)
		}
		defer func() {
			_ = respAuth.Body.Close() //nolint:errcheck // probe response cleanup
		}()

		if respAuth.StatusCode == http.StatusProxyAuthRequired {
			return ConfinementUnenforced, fmt.Errorf("proxy rejected valid session token")
		}
	}

	return ConfinementOK, nil
}
