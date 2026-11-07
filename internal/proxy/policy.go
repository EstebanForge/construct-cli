// Package proxy implements the host-side egress CONNECT proxy and policy engine.
package proxy

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// Network egress tiers.
const (
	TierOpen      = "open"
	TierInternet  = "internet"
	TierAllowlist = "allowlist"
	TierNone      = "none"
)

var blockedCIDRs []*net.IPNet

func init() {
	cidrs := []string{
		"0.0.0.0/8",      // Current network
		"10.0.0.0/8",     // RFC 1918 Private
		"100.64.0.0/10",  // RFC 6598 CGNAT
		"127.0.0.0/8",    // Loopback
		"169.254.0.0/16", // Link-local
		"172.16.0.0/12",  // RFC 1918 Private
		"192.168.0.0/16", // RFC 1918 Private
		"224.0.0.0/4",    // Multicast
		"240.0.0.0/4",    // Reserved
		"::1/128",        // IPv6 Loopback
		"fc00::/7",       // IPv6 Unique Local
		"fe80::/10",      // IPv6 Link-local
		"ff00::/8",       // IPv6 Multicast
	}
	for _, s := range cidrs {
		_, ipNet, err := net.ParseCIDR(s)
		if err == nil {
			blockedCIDRs = append(blockedCIDRs, ipNet)
		}
	}
}

// EcosystemPresets maps ecosystem names to required egress hosts.
var EcosystemPresets = map[string][]string{
	"npm": {
		"registry.npmjs.org",
		"registry.yarnpkg.com",
	},
	"pypi": {
		"pypi.org",
		"files.pythonhosted.org",
	},
	"crates": {
		"crates.io",
		"index.crates.io",
		"static.crates.io",
	},
	"go": {
		"proxy.golang.org",
		"sum.golang.org",
	},
	"github": {
		"github.com",
		"api.github.com",
		"raw.githubusercontent.com",
		"objects.githubusercontent.com",
		"codeload.github.com",
	},
	"apt": {
		"archive.ubuntu.com",
		"security.ubuntu.com",
		"ports.ubuntu.com",
		"deb.debian.org",
	},
}

// Policy evaluates egress requests against network tiers and rules.
type Policy struct {
	mu            sync.RWMutex
	tier          string
	allowlist     map[string]bool
	loopbackPorts map[int]bool
	hostGateway   string
	pinnedHosts   map[string]bool
	resolver      *net.Resolver
}

// NewPolicy constructs an egress policy engine.
func NewPolicy(tier string, allowedHosts []string, loopbackPorts []int, hostGateway string) *Policy {
	if tier == "" {
		tier = TierAllowlist
	}
	allow := make(map[string]bool)
	for _, h := range allowedHosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" {
			allow[h] = true
		}
	}
	// Automatically attach ecosystem presets in allowlist mode.
	for _, hosts := range EcosystemPresets {
		for _, h := range hosts {
			allow[strings.ToLower(h)] = true
		}
	}

	ports := make(map[int]bool)
	for _, p := range loopbackPorts {
		ports[p] = true
	}

	return &Policy{
		tier:          tier,
		allowlist:     allow,
		loopbackPorts: ports,
		hostGateway:   strings.ToLower(hostGateway),
		pinnedHosts:   make(map[string]bool),
		resolver: &net.Resolver{
			PreferGo: true,
		},
	}
}

// AddPinnedHost registers a host as TLS cert-pinned (skips MITM, clean tunnel).
func (p *Policy) AddPinnedHost(host string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pinnedHosts[strings.ToLower(strings.TrimSpace(host))] = true
}

// IsPinned reports whether a host bypasses TLS interception.
func (p *Policy) IsPinned(host string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.pinnedHosts[strings.ToLower(strings.TrimSpace(host))]
}

// IsPrivateIP checks if an IP belongs to private/internal/banned ranges.
func IsPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	// Check IPv4-mapped IPv6 (::ffff:192.168.1.1)
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, cidr := range blockedCIDRs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// CheckDestination resolves host and validates against policy rules.
// Returns validated target IP and port, or an error if blocked.
func (p *Policy) CheckDestination(ctx context.Context, hostPort string) (net.IP, int, error) {
	host, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		host = hostPort
		portStr = "443"
	}
	port, err := net.LookupPort("tcp", portStr)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid port %q: %w", portStr, err)
	}

	lowerHost := strings.ToLower(host)

	// Check host loopback forwarding exception
	if p.isLoopbackRelay(lowerHost, port) {
		return net.ParseIP("127.0.0.1"), port, nil
	}

	p.mu.RLock()
	tier := p.tier
	p.mu.RUnlock()

	if tier == TierNone {
		return nil, 0, fmt.Errorf("egress blocked by tier %q", tier)
	}

	// Verify host is permitted under current tier
	if tier == TierAllowlist {
		if !p.isHostAllowed(lowerHost) {
			return nil, 0, fmt.Errorf("host %q not in allowlist", lowerHost)
		}
	}

	// In open tier, skip DNS blocklist checks
	if tier == TierOpen {
		ips, err := p.resolver.LookupIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, 0, fmt.Errorf("dns lookup failed for %q: %w", host, err)
		}
		return ips[0], port, nil
	}

	// Proxy-side DNS resolution with strict IP verification
	resolveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	ips, err := p.resolver.LookupIP(resolveCtx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, 0, fmt.Errorf("dns resolution failed for %q: %w", host, err)
	}

	for _, ip := range ips {
		if IsPrivateIP(ip) {
			return nil, 0, fmt.Errorf("host %q resolved to prohibited address %s", host, ip.String())
		}
	}

	return ips[0], port, nil
}

func (p *Policy) isHostAllowed(host string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.allowlist[host] {
		return true
	}

	// Wildcard subdomain matching (*.example.com)
	for allowed := range p.allowlist {
		if strings.HasPrefix(allowed, "*.") {
			suffix := allowed[1:]
			if strings.HasSuffix(host, suffix) {
				return true
			}
		}
	}
	return false
}

func (p *Policy) isLoopbackRelay(host string, port int) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.hostGateway != "" && host == p.hostGateway {
		return p.loopbackPorts[port]
	}
	if host == "localhost" || host == "127.0.0.1" || strings.HasSuffix(host, ".localhost") {
		return p.loopbackPorts[port]
	}
	return false
}
