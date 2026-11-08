// Package proxy implements the host-side egress CONNECT proxy and policy engine.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
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
		"::/128",         // IPv6 Unspecified
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
	mu             sync.RWMutex
	tier           string
	allowlist      map[string]bool
	allowSources   map[string]string
	allowedIPs     []net.IP
	blockedDomains map[string]bool
	blockedIPs     []net.IP
	allowedTCP     map[string]bool
	loopbackPorts  map[int]bool
	hostGateway    string
	pinnedHosts    map[string]bool
	resolver       *net.Resolver
}

// NormalizeTier maps legacy mode aliases onto standard network tiers.
func NormalizeTier(tier string) string {
	t := strings.ToLower(strings.TrimSpace(tier))
	switch t {
	case "permissive", "open":
		return TierOpen
	case "strict", "allowlist":
		return TierAllowlist
	case "offline", "none":
		return TierNone
	case "internet":
		return TierInternet
	case "":
		return TierAllowlist
	default:
		return TierNone // Fail closed on unknown or invalid tier
	}
}

// NewPolicy constructs an egress policy engine.
func NewPolicy(tier string, allowedHosts []string, loopbackPorts []int, hostGateway string, allowedTCP ...[]string) *Policy {
	tier = NormalizeTier(tier)
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

	tcpMap := make(map[string]bool)
	if len(allowedTCP) > 0 {
		for _, t := range allowedTCP[0] {
			t = strings.ToLower(strings.TrimSpace(t))
			if t != "" {
				tcpMap[t] = true
			}
		}
	}

	ports := make(map[int]bool)
	for _, p := range loopbackPorts {
		ports[p] = true
	}

	return &Policy{
		tier:           tier,
		allowlist:      allow,
		allowSources:   sources,
		allowedIPs:     nil,
		blockedDomains: make(map[string]bool),
		blockedIPs:     nil,
		allowedTCP:     tcpMap,
		loopbackPorts:  ports,
		hostGateway:    strings.ToLower(hostGateway),
		pinnedHosts:    make(map[string]bool),
		resolver: &net.Resolver{
			PreferGo: true,
		},
	}
}

// SetBlocklists sets the blocked domains and IPs for the policy.
func (p *Policy) SetBlocklists(domains, ips []string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.blockedDomains = make(map[string]bool, len(domains))
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d != "" {
			p.blockedDomains[d] = true
		}
	}

	p.blockedIPs = make([]net.IP, 0, len(ips))
	for _, ipStr := range ips {
		ipStr = strings.TrimSpace(ipStr)
		if parsed := net.ParseIP(ipStr); parsed != nil {
			p.blockedIPs = append(p.blockedIPs, parsed)
		}
	}
}

// SetAllowedIPs sets the explicit allowed IPs for the policy.
func (p *Policy) SetAllowedIPs(ips []string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.allowedIPs = make([]net.IP, 0, len(ips))
	for _, ipStr := range ips {
		ipStr = strings.TrimSpace(ipStr)
		if parsed := net.ParseIP(ipStr); parsed != nil {
			p.allowedIPs = append(p.allowedIPs, parsed)
		}
	}
}

func (p *Policy) isDomainBlocked(host string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.blockedDomains[host] {
		return true
	}
	for blocked := range p.blockedDomains {
		if strings.HasPrefix(blocked, "*.") {
			if strings.HasSuffix(host, blocked[1:]) {
				return true
			}
		}
	}
	return false
}

func (p *Policy) isIPExplicitlyBlocked(ip net.IP) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, b := range p.blockedIPs {
		if b.Equal(ip) {
			return true
		}
	}
	return false
}

func (p *Policy) isIPExplicitlyAllowed(ip net.IP) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, a := range p.allowedIPs {
		if a.Equal(ip) {
			return true
		}
	}
	return false
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

// IsTCPAllowed reports whether host:port matches an explicit allowed_tcp rule.
func (p *Policy) IsTCPAllowed(host string, port int) bool {
	return p.isTCPAllowed(host, port)
}

func (p *Policy) isTCPAllowed(host string, port int) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	lowerHost := strings.ToLower(strings.TrimSpace(host))
	key := fmt.Sprintf("%s:%d", lowerHost, port)
	if p.allowedTCP[key] {
		return true
	}

	for entry := range p.allowedTCP {
		h, pStr, err := net.SplitHostPort(entry)
		if err != nil {
			continue
		}
		if pNum, err := strconv.Atoi(pStr); err == nil && pNum == port {
			if strings.HasPrefix(h, "*.") && strings.HasSuffix(lowerHost, h[1:]) {
				return true
			}
		}
	}
	return false
}

// IsRawTCP reports whether the destination bypasses TLS MITM and pipes raw TCP stream.
func (p *Policy) IsRawTCP(host string, port int) bool {
	if p.isTCPAllowed(host, port) {
		return true
	}
	if p.isHostAllowed(host) || p.isLoopbackRelay(host, port) {
		return false
	}
	return port != 443 && port != 8443
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
	lowerHost = strings.TrimSuffix(lowerHost, ".")
	p.mu.RLock()
	tier := p.tier
	p.mu.RUnlock()

	decision := PolicyDecision{
		TargetHost: lowerHost,
		TargetPort: port,
		Tier:       tier,
	}

	// 1. Tier 'none' blocks all egress immediately (including loopback relay).
	if tier == TierNone {
		decision.Allowed = false
		decision.Reason = "egress blocked by tier 'none'"
		return decision
	}

	// 2. Blocklist check: blocked domains (takes precedence over relays and allowlists).
	if p.isDomainBlocked(lowerHost) {
		decision.Allowed = false
		decision.Reason = fmt.Sprintf("host %q is blocked by network policy", lowerHost)
		return decision
	}

	// 3. Blocklist check: blocked IPs.
	if parsedIP := net.ParseIP(host); parsedIP != nil {
		if p.isIPExplicitlyBlocked(parsedIP) {
			decision.Allowed = false
			decision.Reason = fmt.Sprintf("ip %s is blocked by network policy", host)
			return decision
		}
	}

	// 4. Loopback relays for host dev site routing.
	if p.isLoopbackRelay(lowerHost, port) {
		decision.Allowed = true
		decision.Source = "loopback_relay"
		decision.ResolvedIPs = []net.IP{net.ParseIP("127.0.0.1")}
		return decision
	}

	// 5. Allowed TCP rules.
	if p.isTCPAllowed(lowerHost, port) {
		decision.Source = "allowed_tcp"
		if parsed := net.ParseIP(host); parsed != nil {
			decision.Allowed = true
			decision.ResolvedIPs = []net.IP{parsed}
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
			if p.isIPExplicitlyBlocked(ip) {
				decision.Allowed = false
				decision.Reason = fmt.Sprintf("host %q resolved to blocked ip %s", host, ip.String())
				return decision
			}
		}
		decision.Allowed = true
		decision.ResolvedIPs = ips
		return decision
	}

	// 6. Tier allowlist evaluation.
	if tier == TierAllowlist {
		allowed, src := p.matchHost(lowerHost)
		if !allowed {
			parsedIP := net.ParseIP(host)
			if parsedIP != nil && p.isIPExplicitlyAllowed(parsedIP) {
				src = "allowed_ips"
			} else {
				decision.Allowed = false
				decision.Reason = fmt.Sprintf("host %q not in allowlist", lowerHost)
				return decision
			}
		}
		decision.Source = src
	} else {
		decision.Source = "tier:" + tier
	}

	// 7. IP resolution and private IP check (applies to ALL public tiers: open, internet, allowlist).
	if parsed := net.ParseIP(host); parsed != nil {
		if p.isIPExplicitlyBlocked(parsed) {
			decision.Allowed = false
			decision.Reason = fmt.Sprintf("ip %s is blocked by network policy", host)
			return decision
		}
		if !p.isIPExplicitlyAllowed(parsed) && IsPrivateIP(parsed) {
			decision.Allowed = false
			decision.Reason = fmt.Sprintf("host %q is a prohibited address %s", host, parsed.String())
			return decision
		}
		decision.Allowed = true
		decision.ResolvedIPs = []net.IP{parsed}
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
		if p.isIPExplicitlyBlocked(ip) {
			decision.Allowed = false
			decision.Reason = fmt.Sprintf("host %q resolved to blocked ip %s", host, ip.String())
			return decision
		}
		if !p.isIPExplicitlyAllowed(ip) && IsPrivateIP(ip) {
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
