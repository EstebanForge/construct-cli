// Package proxy implements the host-side egress CONNECT proxy and policy engine.
package proxy

import (
	"context"
	"errors"
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

// PolicyDecision represents the evaluated access decision for an egress target.
type PolicyDecision struct {
	TargetHost  string
	TargetPort  int
	Tier        string
	Allowed     bool
	Source      string
	ResolvedIPs []net.IP
	Reason      string
}

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
	allowSources  map[string]string
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
	sources := make(map[string]string)
	for _, h := range allowedHosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" {
			allow[h] = true
			sources[h] = "config"
		}
	}
	// Automatically attach ecosystem presets in allowlist mode.
	for presetName, hosts := range EcosystemPresets {
		for _, h := range hosts {
			lower := strings.ToLower(h)
			allow[lower] = true
			if sources[lower] == "" {
				sources[lower] = "preset:" + presetName
			}
		}
	}

	ports := make(map[int]bool)
	for _, p := range loopbackPorts {
		ports[p] = true
	}

	return &Policy{
		tier:          tier,
		allowlist:     allow,
		allowSources:  sources,
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

// Explain evaluates an egress target against active policy tiers and returns the detailed decision.
func (p *Policy) Explain(ctx context.Context, hostPort string) PolicyDecision {
	host, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		host = hostPort
		portStr = "443"
	}
	port, err := net.LookupPort("tcp", portStr)
	if err != nil {
		return PolicyDecision{
			TargetHost: host,
			Allowed:    false,
			Reason:     fmt.Sprintf("invalid port %q: %v", portStr, err),
		}
	}

	lowerHost := strings.ToLower(host)
	p.mu.RLock()
	tier := p.tier
	p.mu.RUnlock()

	decision := PolicyDecision{
		TargetHost: lowerHost,
		TargetPort: port,
		Tier:       tier,
	}

	if p.isLoopbackRelay(lowerHost, port) {
		decision.Allowed = true
		decision.Source = "loopback_relay"
		decision.ResolvedIPs = []net.IP{net.ParseIP("127.0.0.1")}
		return decision
	}

	if tier == TierNone {
		decision.Allowed = false
		decision.Reason = "egress blocked by tier 'none'"
		return decision
	}

	if tier == TierAllowlist {
		allowed, src := p.matchHost(lowerHost)
		if !allowed {
			decision.Allowed = false
			decision.Reason = fmt.Sprintf("host %q not in allowlist", lowerHost)
			return decision
		}
		decision.Source = src
	} else {
		decision.Source = "tier:" + tier
	}

	if tier == TierOpen {
		ips, err := p.resolver.LookupIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			decision.Allowed = false
			decision.Reason = fmt.Sprintf("dns lookup failed for %q: %v", host, err)
			return decision
		}
		decision.Allowed = true
		decision.ResolvedIPs = ips
		return decision
	}

	resolveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	ips, err := p.resolver.LookupIP(resolveCtx, "ip", host)
	if err != nil || len(ips) == 0 {
		decision.Allowed = false
		decision.Reason = fmt.Sprintf("dns resolution failed for %q: %v", host, err)
		return decision
	}

	for _, ip := range ips {
		if IsPrivateIP(ip) {
			decision.Allowed = false
			decision.Reason = fmt.Sprintf("host %q resolved to prohibited address %s", host, ip.String())
			return decision
		}
	}

	decision.Allowed = true
	decision.ResolvedIPs = ips
	return decision
}

// CheckDestination resolves host and validates against policy rules.
// Returns validated target IP and port, or an error if blocked.
func (p *Policy) CheckDestination(ctx context.Context, hostPort string) (net.IP, int, error) {
	decision := p.Explain(ctx, hostPort)
	if !decision.Allowed {
		return nil, 0, errors.New(decision.Reason)
	}
	if len(decision.ResolvedIPs) == 0 {
		return net.ParseIP("127.0.0.1"), decision.TargetPort, nil
	}
	return decision.ResolvedIPs[0], decision.TargetPort, nil
}

func (p *Policy) matchHost(host string) (bool, string) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.allowlist[host] {
		src := p.allowSources[host]
		if src == "" {
			src = "allowlist"
		}
		return true, src
	}

	// Wildcard subdomain matching (*.example.com)
	for allowed, src := range p.allowSources {
		if strings.HasPrefix(allowed, "*.") {
			suffix := allowed[1:]
			if strings.HasSuffix(host, suffix) {
				return true, fmt.Sprintf("%s (%s)", src, allowed)
			}
		}
	}
	return false, ""
}

func (p *Policy) isHostAllowed(host string) bool {
	allowed, _ := p.matchHost(host)
	return allowed
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
