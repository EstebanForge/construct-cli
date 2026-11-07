// Package network manages allow/block rules and runtime network configuration.
package network

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/proxy"
	"github.com/EstebanForge/construct-cli/internal/runtime"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// AddRule adds a network allow/block rule
func AddRule(target, action string) {
	// 1. Validate input
	ruleType, err := ValidateTarget(target)
	if err != nil {
		ui.GumError(fmt.Sprintf("Invalid target '%s': %v", target, err))
		os.Exit(1)
	}

	// 2. Load config
	cfg, _, err := config.Load()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to load config: %v", err))
		os.Exit(1)
	}

	// 3. Check conflicts
	if err := CheckRuleConflicts(cfg, target, action); err != nil {
		ui.GumError(err.Error())
		os.Exit(1)
	}

	// 4. Resolve domain with spinner if Gum available
	var resolvedIPs []string
	if ruleType == "domain" {
		resolvedIPs = ui.GumSpinner(
			fmt.Sprintf("Resolving %s...", target),
			func() []string {
				ips, err := ResolveDomain(target)
				if err != nil {
					return nil
				}
				return ips
			},
		)

		if len(resolvedIPs) > 0 {
			ui.GumSuccess(fmt.Sprintf("Resolved to %s", strings.Join(resolvedIPs, ", ")))
		} else {
			ui.GumWarning("Could not resolve domain")
		}
	}

	// 5. ALWAYS update config.toml
	if err := AddRuleToConfig(cfg, target, action, ruleType); err != nil {
		ui.GumError(fmt.Sprintf("Failed to update config: %v", err))
		os.Exit(1)
	}
	ui.GumSuccess("Updated config.toml")

	if cfg.Runtime.Backend == config.BackendMsb {
		applyRuleMsb(target, action, resolvedIPs)
		return
	}

	// 6. Apply to running container if exists
	if err := runtime.ValidateBackendSelected(cfg); err != nil {
		ui.GumError(err.Error())
		os.Exit(1)
	}
	containerRuntime := runtime.ResolveContainerRuntime(cfg)
	for _, containerName := range runningSessionContainers(containerRuntime) {
		if err := ApplyRuleToContainer(containerRuntime, containerName,
			target, action, resolvedIPs); err != nil {
			ui.GumWarning(fmt.Sprintf("Could not apply to container '%s'", containerName))
			ui.InfoLn("   Rule will apply on next container start")
		} else {
			ui.GumSuccess(fmt.Sprintf("Applied to container '%s' immediately", containerName))
		}
	}
	if len(runningSessionContainers(containerRuntime)) == 0 {
		ui.GumInfo("No running container. Rule will apply on next start.")
	}
}

// RemoveRule removes a network rule
func RemoveRule(target string) {
	// 1. Load config
	cfg, _, err := config.Load()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to load config: %v", err))
		os.Exit(1)
	}

	// 2. Check if rule exists
	found := false
	for _, domain := range cfg.Network.AllowedDomains {
		if domain == target {
			found = true
			break
		}
	}
	for _, ip := range cfg.Network.AllowedIPs {
		if ip == target {
			found = true
			break
		}
	}
	for _, tcp := range cfg.Network.AllowedTCP {
		if tcp == target {
			found = true
			break
		}
	}
	for _, domain := range cfg.Network.BlockedDomains {
		if domain == target {
			found = true
			break
		}
	}
	for _, ip := range cfg.Network.BlockedIPs {
		if ip == target {
			found = true
			break
		}
	}

	if !found {
		ui.GumWarning(fmt.Sprintf("Rule '%s' not found in config", target))
		os.Exit(1)
	}

	// 3. Remove from config
	if err := RemoveRuleFromConfig(cfg, target); err != nil {
		ui.GumError(fmt.Sprintf("Failed to update config: %v", err))
		os.Exit(1)
	}
	ui.GumSuccess("Removed from config.toml")

	if cfg.Runtime.Backend == config.BackendMsb {
		removeRuleMsb(target)
		return
	}

	// 4. Remove from running container if exists
	if err := runtime.ValidateBackendSelected(cfg); err != nil {
		ui.GumError(err.Error())
		os.Exit(1)
	}
	containerRuntime := runtime.ResolveContainerRuntime(cfg)
	for _, containerName := range runningSessionContainers(containerRuntime) {
		if err := RemoveRuleFromContainer(containerRuntime, containerName, target); err != nil {
			ui.GumWarning(fmt.Sprintf("Could not remove from container '%s'", containerName))
			ui.InfoLn("   Rule will be removed on next container start")
		} else {
			ui.GumSuccess(fmt.Sprintf("Removed from container '%s' immediately", containerName))
		}
	}
	if len(runningSessionContainers(containerRuntime)) == 0 {
		ui.GumInfo("No running container. Rule will be removed on next start.")
	}
}

// ListRules lists all configured network rules
func ListRules() {
	cfg, _, err := config.Load()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to load config: %v", err))
		os.Exit(1)
	}

	ui.InfoLn()
	cmd := ui.GetGumCommand("style", "--border", "rounded",
		"--padding", "1 2", "--bold", "Network Configuration")
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to render header: %v\n", err)
	}
	ui.InfoLn()

	cmd = ui.GetGumCommand("style", "--foreground", "212",
		fmt.Sprintf("Mode: %s", cfg.Network.Mode))
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to render mode: %v\n", err)
	}
	ui.InfoLn()

	// Allowed Domains
	if len(cfg.Network.AllowedDomains) > 0 {
		cmd = ui.GetGumCommand("style", "--foreground", "86", "--bold", "Allowed Domains:")
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render allowed domains header: %v\n", err)
		}

		for _, domain := range cfg.Network.AllowedDomains {
			ips, err := ResolveDomain(domain)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to resolve %s: %v\n", domain, err)
			}
			if len(ips) > 0 && err == nil {
				cmd = ui.GetGumCommand("style", "--foreground", "242",
					fmt.Sprintf("  • %s → %s", domain, strings.Join(ips, ", ")))
			} else {
				cmd = ui.GetGumCommand("style", "--foreground", "242",
					fmt.Sprintf("  • %s (unresolved)", domain))
			}
			cmd.Stdout = os.Stdout
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to render allowed domain: %v\n", err)
			}
		}
		ui.InfoLn()
	}

	// Allowed IPs
	if len(cfg.Network.AllowedIPs) > 0 {
		cmd = ui.GetGumCommand("style", "--foreground", "86", "--bold", "Allowed IPs:")
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render allowed IPs header: %v\n", err)
		}
		for _, ip := range cfg.Network.AllowedIPs {
			cmd = ui.GetGumCommand("style", "--foreground", "242",
				fmt.Sprintf("  • %s", ip))
			cmd.Stdout = os.Stdout
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to render allowed IP: %v\n", err)
			}
		}
		ui.InfoLn()
	}

	// Allowed TCP Services
	if len(cfg.Network.AllowedTCP) > 0 {
		cmd = ui.GetGumCommand("style", "--foreground", "39", "--bold", "Allowed TCP Services:")
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render allowed TCP header: %v\n", err)
		}
		for _, tcp := range cfg.Network.AllowedTCP {
			cmd = ui.GetGumCommand("style", "--foreground", "242",
				fmt.Sprintf("  • %s", tcp))
			cmd.Stdout = os.Stdout
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to render allowed TCP service: %v\n", err)
			}
		}
		ui.InfoLn()
	}

	// Blocked rules (red)
	if len(cfg.Network.BlockedDomains) > 0 || len(cfg.Network.BlockedIPs) > 0 {
		cmd = ui.GetGumCommand("style", "--foreground", "196", "--bold", "Blocked:")
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render blocked header: %v\n", err)
		}

		for _, domain := range cfg.Network.BlockedDomains {
			cmd = ui.GetGumCommand("style", "--foreground", "242",
				fmt.Sprintf("  • %s", domain))
			cmd.Stdout = os.Stdout
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to render blocked domain: %v\n", err)
			}
		}
		for _, ip := range cfg.Network.BlockedIPs {
			cmd = ui.GetGumCommand("style", "--foreground", "242",
				fmt.Sprintf("  • %s", ip))
			cmd.Stdout = os.Stdout
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to render blocked IP: %v\n", err)
			}
		}
		ui.InfoLn()
	}

	if len(cfg.Network.AllowedDomains) == 0 && len(cfg.Network.AllowedIPs) == 0 &&
		len(cfg.Network.AllowedTCP) == 0 &&
		len(cfg.Network.BlockedDomains) == 0 && len(cfg.Network.BlockedIPs) == 0 {
		ui.GumInfo("No network rules configured")
	}
}

// ListRulesBasic lists rules without Gum (fallback)
func ListRulesBasic(cfg *config.Config) {
	ui.InfoLn("\n=== Network Configuration ===")
	ui.InfoF("Mode: %s\n\n", cfg.Network.Mode)

	if len(cfg.Network.AllowedDomains) > 0 {
		ui.InfoLn("Allowed Domains:")
		for _, domain := range cfg.Network.AllowedDomains {
			ui.InfoF("  • %s\n", domain)
		}
		ui.InfoLn()
	}

	if len(cfg.Network.AllowedIPs) > 0 {
		ui.InfoLn("Allowed IPs:")
		for _, ip := range cfg.Network.AllowedIPs {
			ui.InfoF("  • %s\n", ip)
		}
		ui.InfoLn()
	}

	if len(cfg.Network.AllowedTCP) > 0 {
		ui.InfoLn("Allowed TCP Services:")
		for _, tcp := range cfg.Network.AllowedTCP {
			ui.InfoF("  • %s\n", tcp)
		}
		ui.InfoLn()
	}

	if len(cfg.Network.BlockedDomains) > 0 || len(cfg.Network.BlockedIPs) > 0 {
		ui.InfoLn("Blocked:")
		for _, domain := range cfg.Network.BlockedDomains {
			ui.InfoF("  • %s\n", domain)
		}
		for _, ip := range cfg.Network.BlockedIPs {
			ui.InfoF("  • %s\n", ip)
		}
		ui.InfoLn()
	}

	if len(cfg.Network.AllowedDomains) == 0 && len(cfg.Network.AllowedIPs) == 0 &&
		len(cfg.Network.AllowedTCP) == 0 &&
		len(cfg.Network.BlockedDomains) == 0 && len(cfg.Network.BlockedIPs) == 0 {
		ui.InfoLn("No network rules configured")
	}
}

// ShowStatus shows the active UFW status in the container
func ShowStatus() {
	cfg, _, err := config.Load()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to load config: %v", err))
		os.Exit(1)
	}
	if cfg.Runtime.Backend == config.BackendMsb {
		showStatusMsb()
		return
	}
	if err := runtime.ValidateBackendSelected(cfg); err != nil {
		ui.GumError(err.Error())
		os.Exit(1)
	}
	containerRuntime := runtime.ResolveContainerRuntime(cfg)
	containers := runningSessionContainers(containerRuntime)

	if len(containers) == 0 {
		ui.GumWarning("Container is not running")
		ui.InfoLn("Start an agent to see active network status")
		os.Exit(1)
	}

	containerName := containers[0]

	ui.InfoLn("=== Active UFW Status in Container ===")
	ui.InfoLn()

	var cmd *exec.Cmd
	switch containerRuntime {
	case "docker", "container":
		cmd = exec.Command("docker", "exec", containerName, "sh", "-c", "if [ -x /usr/local/bin/network-filter.sh ]; then /usr/local/bin/network-filter.sh show_status; else echo 'Host egress proxy active; in-guest filter script not installed'; fi")
	case "podman":
		cmd = exec.Command("podman", "exec", containerName, "sh", "-c", "if [ -x /usr/local/bin/network-filter.sh ]; then /usr/local/bin/network-filter.sh show_status; else echo 'Host egress proxy active; in-guest filter script not installed'; fi")
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		ui.GumError(fmt.Sprintf("Failed to get status: %v", err))
		os.Exit(1)
	}
}

// ClearRules clears all network rules with confirmation
func ClearRules() {
	if !ui.GumConfirm("Clear ALL network rules?") {
		ui.InfoLn("Canceled.")
		return
	}

	cfg, _, err := config.Load()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to load config: %v", err))
		os.Exit(1)
	}

	// Clear all rules
	cfg.Network.AllowedDomains = []string{}
	cfg.Network.AllowedIPs = []string{}
	cfg.Network.BlockedDomains = []string{}
	cfg.Network.BlockedIPs = []string{}

	if err := cfg.Save(); err != nil {
		ui.GumError(fmt.Sprintf("Failed to save config: %v", err))
		os.Exit(1)
	}

	ui.GumSuccess("All network rules cleared from config")
	ui.GumWarning("Restart container for changes to take effect")
}

// ValidateTarget validates and determines the type of a network target
func ValidateTarget(target string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("target cannot be empty")
	}

	// Check if it's a valid IP
	if IsValidIP(target) {
		return "ip", nil
	}

	// Check if it's a valid CIDR
	if IsValidCIDR(target) {
		return "cidr", nil
	}

	// Check if it's a valid domain
	if IsValidDomain(target) {
		return "domain", nil
	}

	// Check if it's a valid host:port TCP service
	if host, portStr, err := net.SplitHostPort(target); err == nil {
		if port, err := strconv.Atoi(portStr); err == nil && port > 0 && port <= 65535 {
			if IsValidDomain(host) || IsValidIP(host) {
				return "tcp", nil
			}
		}
	}

	return "", fmt.Errorf("invalid target: must be IP, CIDR, domain, or host:port TCP service")
}

// IsValidIP checks if a string is a valid IPv4 address
func IsValidIP(target string) bool {
	parts := strings.Split(target, ".")
	if len(parts) != 4 {
		return false
	}

	for _, part := range parts {
		num, err := strconv.Atoi(part)
		if err != nil || num < 0 || num > 255 {
			return false
		}
	}

	return true
}

// IsValidCIDR checks if a string is a valid CIDR notation
func IsValidCIDR(target string) bool {
	parts := strings.Split(target, "/")
	if len(parts) != 2 {
		return false
	}

	// Validate IP part
	if !IsValidIP(parts[0]) {
		return false
	}

	// Validate prefix length
	prefix, err := strconv.Atoi(parts[1])
	if err != nil || prefix < 0 || prefix > 32 {
		return false
	}

	return true
}

// IsValidDomain checks if a string is a valid domain name
func IsValidDomain(target string) bool {
	// Allow wildcard domains
	target = strings.TrimPrefix(target, "*.")

	// Domain must have at least one dot and valid characters
	if !strings.Contains(target, ".") {
		return false
	}

	// Check for valid domain characters
	for _, char := range target {
		if (char < 'a' || char > 'z') &&
			(char < 'A' || char > 'Z') &&
			(char < '0' || char > '9') &&
			char != '.' && char != '-' {
			return false
		}
	}

	// Domain parts should not start or end with hyphen
	parts := strings.Split(target, ".")
	for _, part := range parts {
		if part == "" || strings.HasPrefix(part, "-") || strings.HasSuffix(part, "-") {
			return false
		}
	}

	return true
}

// ResolveDomain resolves a domain to IP addresses using dig
func ResolveDomain(domain string) ([]string, error) {
	// Remove wildcard prefix for resolution
	resolveDomain := domain
	if strings.HasPrefix(domain, "*.") {
		resolveDomain = domain[2:]
	}

	cmd := exec.Command("dig", "+short", resolveDomain)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve domain: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var ips []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && IsValidIP(line) {
			ips = append(ips, line)
		}
	}

	return ips, nil
}

// CheckRuleConflicts checks for conflicts between new rule and existing config
func CheckRuleConflicts(cfg *config.Config, target, action string) error {
	switch action {
	case "allow":
		// Check if already in allowlist
		for _, domain := range cfg.Network.AllowedDomains {
			if domain == target {
				return fmt.Errorf("'%s' is already in allowed domains", target)
			}
		}
		for _, ip := range cfg.Network.AllowedIPs {
			if ip == target {
				return fmt.Errorf("'%s' is already in allowed IPs", target)
			}
		}
		for _, tcp := range cfg.Network.AllowedTCP {
			if tcp == target {
				return fmt.Errorf("'%s' is already in allowed TCP services", target)
			}
		}

		// Check if in blocklist (conflict)
		for _, domain := range cfg.Network.BlockedDomains {
			if domain == target {
				return fmt.Errorf("'%s' is in blocked domains. Remove it first or use 'remove' command", target)
			}
		}
		for _, ip := range cfg.Network.BlockedIPs {
			if ip == target {
				return fmt.Errorf("'%s' is in blocked IPs. Remove it first or use 'remove' command", target)
			}
		}

	case "block":
		// Check if already in blocklist
		for _, domain := range cfg.Network.BlockedDomains {
			if domain == target {
				return fmt.Errorf("'%s' is already in blocked domains", target)
			}
		}
		for _, ip := range cfg.Network.BlockedIPs {
			if ip == target {
				return fmt.Errorf("'%s' is already in blocked IPs", target)
			}
		}

		// Check if in allowlist (conflict)
		for _, domain := range cfg.Network.AllowedDomains {
			if domain == target {
				return fmt.Errorf("'%s' is in allowed domains. Remove it first or use 'remove' command", target)
			}
		}
		for _, ip := range cfg.Network.AllowedIPs {
			if ip == target {
				return fmt.Errorf("'%s' is in allowed IPs. Remove it first or use 'remove' command", target)
			}
		}
		for _, tcp := range cfg.Network.AllowedTCP {
			if tcp == target {
				return fmt.Errorf("'%s' is in allowed TCP services. Remove it first or use 'remove' command", target)
			}
		}
	}

	return nil
}

// AddRuleToConfig adds a rule to the config and saves it
func AddRuleToConfig(cfg *config.Config, target, action, ruleType string) error {
	switch action {
	case "allow":
		switch ruleType {
		case "domain":
			cfg.Network.AllowedDomains = AppendUnique(cfg.Network.AllowedDomains, target)
		case "tcp":
			cfg.Network.AllowedTCP = AppendUnique(cfg.Network.AllowedTCP, target)
		default:
			cfg.Network.AllowedIPs = AppendUnique(cfg.Network.AllowedIPs, target)
		}
	case "block":
		if ruleType == "tcp" {
			return fmt.Errorf("blocking specific TCP ports is not supported; block the host or domain instead")
		}
		if ruleType == "domain" {
			cfg.Network.BlockedDomains = AppendUnique(cfg.Network.BlockedDomains, target)
		} else {
			cfg.Network.BlockedIPs = AppendUnique(cfg.Network.BlockedIPs, target)
		}
	default:
		return fmt.Errorf("invalid action: %s", action)
	}

	return cfg.Save()
}

// RemoveRuleFromConfig removes a rule from the config and saves it
func RemoveRuleFromConfig(cfg *config.Config, target string) error {
	// Remove from all possible locations
	cfg.Network.AllowedDomains = RemoveString(cfg.Network.AllowedDomains, target)
	cfg.Network.AllowedIPs = RemoveString(cfg.Network.AllowedIPs, target)
	cfg.Network.AllowedTCP = RemoveString(cfg.Network.AllowedTCP, target)
	cfg.Network.BlockedDomains = RemoveString(cfg.Network.BlockedDomains, target)
	cfg.Network.BlockedIPs = RemoveString(cfg.Network.BlockedIPs, target)

	return cfg.Save()
}

// AppendUnique adds an item to a slice if it doesn't already exist
func AppendUnique(slice []string, item string) []string {
	for _, existing := range slice {
		if existing == item {
			return slice
		}
	}
	return append(slice, item)
}

// RemoveString removes all occurrences of an item from a slice
func RemoveString(slice []string, item string) []string {
	result := []string{}
	for _, existing := range slice {
		if existing != item {
			result = append(result, existing)
		}
	}
	return result
}

// ApplyRuleToContainer applies a network rule to a running container
func ApplyRuleToContainer(containerRuntime, containerName, target, action string, resolvedIPs []string) error {
	switch action {
	case "allow":
		// Apply allow rules for all resolved IPs
		ipsToApply := resolvedIPs
		if len(ipsToApply) == 0 {
			// If no resolved IPs (e.g., it's a direct IP), use the target
			ipsToApply = []string{target}
		}

		for _, ip := range ipsToApply {
			if err := ExecUFWCommand(containerRuntime, containerName, "add_allow_rule", ip); err != nil {
				return fmt.Errorf("failed to apply allow rule for %s: %w", ip, err)
			}
		}
	case "block":
		// Apply deny rules for all resolved IPs
		ipsToApply := resolvedIPs
		if len(ipsToApply) == 0 {
			ipsToApply = []string{target}
		}

		for _, ip := range ipsToApply {
			if err := ExecUFWCommand(containerRuntime, containerName, "add_deny_rule", ip); err != nil {
				return fmt.Errorf("failed to apply deny rule for %s: %w", ip, err)
			}
		}
	}

	return nil
}

// RemoveRuleFromContainer removes a network rule from a running container
func RemoveRuleFromContainer(containerRuntime, containerName, target string) error {
	// Try to resolve if it's a domain
	var ipsToRemove []string
	if !IsValidIP(target) && !IsValidCIDR(target) {
		// It's a domain, try to resolve
		ips, err := ResolveDomain(target)
		if err != nil {
			ui.LogWarning("Failed to resolve %s: %v", target, err)
		} else {
			ipsToRemove = ips
		}
	}

	if len(ipsToRemove) == 0 {
		ipsToRemove = []string{target}
	}

	for _, ip := range ipsToRemove {
		if err := ExecUFWCommand(containerRuntime, containerName, "remove_rule", ip); err != nil {
			return fmt.Errorf("failed to remove rule for %s: %w", ip, err)
		}
	}

	return nil
}

// ExecUFWCommand executes a network command in the container (deprecated; egress proxy handles enforcement).
func ExecUFWCommand(containerRuntime, containerName, command, arg string) error {
	var cmd *exec.Cmd

	switch containerRuntime {
	case "docker", "container":
		cmd = exec.Command("docker", "exec", containerName, "sh", "-c", "if [ -x /usr/local/bin/network-filter.sh ]; then /usr/local/bin/network-filter.sh \"$@\"; else echo 'error: network-filter.sh not found' >&2; exit 1; fi", "--", command, arg)
	case "podman":
		cmd = exec.Command("podman", "exec", containerName, "sh", "-c", "if [ -x /usr/local/bin/network-filter.sh ]; then /usr/local/bin/network-filter.sh \"$@\"; else echo 'error: network-filter.sh not found' >&2; exit 1; fi", "--", command, arg)
	default:
		return fmt.Errorf("unsupported runtime: %s", containerRuntime)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("network command failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// runningSessionContainers returns all running session containers (CWD-derived
// names like construct-cli-<hash> plus the legacy singleton construct-cli).
func runningSessionContainers(containerRuntime string) []string {
	var names []string
	for _, name := range runtime.ListContainersByPrefix(containerRuntime, "construct-cli-") {
		if name != "construct-cli-daemon" && runtime.IsContainerRunning(containerRuntime, name) {
			names = append(names, name)
		}
	}
	// Legacy singleton container
	if runtime.IsContainerRunning(containerRuntime, "construct-cli") {
		names = append(names, "construct-cli")
	}
	return names
}

func applyRuleMsb(target, action string, resolvedIPs []string) {
	ctx := context.Background()
	m := runtime.NewMsbBackend()
	state, err := m.State(ctx, "construct-cli-daemon")
	if err != nil || state != runtime.ContainerStateRunning {
		ui.GumInfo("No running sandbox. Rule will apply on next start.")
		return
	}
	var ips []string
	if len(resolvedIPs) > 0 {
		ips = resolvedIPs
	} else if IsValidIP(target) || IsValidCIDR(target) {
		ips = []string{target}
	}
	for _, ip := range ips {
		cmd := "add_allow"
		if action == "block" {
			cmd = "add_block"
		}
		_, code, err := m.Exec(ctx, runtime.ExecOptions{
			Name:    "construct-cli-daemon",
			Command: []string{"sh", "-c", "if [ -x /usr/local/bin/network-filter.sh ]; then /usr/local/bin/network-filter.sh \"$@\"; else echo 'error: network-filter.sh not found' >&2; exit 1; fi", "--", cmd, ip},
			User:    "root",
		})
		if err != nil || code != 0 {
			ui.GumWarning("Could not apply to daemon sandbox")
			ui.InfoLn("   Rule will apply on next start")
			return
		}
	}
	ui.GumSuccess("Applied to sandbox 'construct-cli-daemon' immediately")
}

func removeRuleMsb(target string) {
	ctx := context.Background()
	m := runtime.NewMsbBackend()
	state, err := m.State(ctx, "construct-cli-daemon")
	if err != nil || state != runtime.ContainerStateRunning {
		ui.GumInfo("No running sandbox. Rule will apply on next start.")
		return
	}
	var ipsToRemove []string
	if !IsValidIP(target) && !IsValidCIDR(target) {
		if ips, err := ResolveDomain(target); err == nil {
			ipsToRemove = ips
		}
	}
	if len(ipsToRemove) == 0 {
		ipsToRemove = []string{target}
	}
	for _, ip := range ipsToRemove {
		_, code, err := m.Exec(ctx, runtime.ExecOptions{
			Name:    "construct-cli-daemon",
			Command: []string{"sh", "-c", "if [ -x /usr/local/bin/network-filter.sh ]; then /usr/local/bin/network-filter.sh \"$@\"; else echo 'error: network-filter.sh not found' >&2; exit 1; fi", "--", "remove_rule", ip},
			User:    "root",
		})
		if err != nil || code != 0 {
			ui.GumWarning("Could not remove from daemon sandbox")
			ui.InfoLn("   Rule will be removed on next start")
			return
		}
	}
	ui.GumSuccess("Removed from sandbox 'construct-cli-daemon' immediately")
}

func showStatusMsb() {
	ctx := context.Background()
	m := runtime.NewMsbBackend()
	state, err := m.State(ctx, "construct-cli-daemon")
	if err != nil || state != runtime.ContainerStateRunning {
		ui.GumWarning("Sandbox is not running")
		ui.InfoLn("Start an agent to see active network status")
		os.Exit(1)
	}
	ui.InfoLn("=== Active Network Status in Sandbox ===")
	ui.InfoLn()
	_, err = m.ExecStream(ctx, runtime.ExecOptions{
		Name:    "construct-cli-daemon",
		Command: []string{"sh", "-c", "if [ -x /usr/local/bin/network-filter.sh ]; then /usr/local/bin/network-filter.sh show_status; else echo 'Host egress proxy active; in-guest filter script not installed'; fi"},
		User:    "root",
	})
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to get status: %v", err))
		os.Exit(1)
	}
}

// Explain diagnoses whether an egress destination is allowed or blocked by the proxy policy.
func Explain(target string) {
	if target == "" {
		ui.GumError("Usage: construct network explain <host[:port]>")
		return
	}

	cfg, _, err := config.Load()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to load config: %v", err))
		return
	}

	p := proxy.NewPolicy(cfg.Network.Mode, cfg.Network.AllowedDomains, cfg.Sandbox.HostLoopbackPorts, "", cfg.Network.AllowedTCP)
	p.SetBlocklists(cfg.Network.BlockedDomains, cfg.Network.BlockedIPs)
	p.SetAllowedIPs(cfg.Network.AllowedIPs)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	decision := p.Explain(ctx, target)

	fmt.Println()
	fmt.Printf("Destination: %s:%d\n", decision.TargetHost, decision.TargetPort)
	fmt.Printf("Active Tier: %s\n", decision.Tier)

	if decision.Allowed {
		fmt.Printf("Decision:    \033[32mALLOWED\033[0m\n")
		if decision.Source != "" {
			fmt.Printf("Rule Source: %s\n", decision.Source)
		}
		if len(decision.ResolvedIPs) > 0 {
			var ipStrs []string
			for _, ip := range decision.ResolvedIPs {
				ipStrs = append(ipStrs, ip.String())
			}
			fmt.Printf("Resolved IP: %s\n", strings.Join(ipStrs, ", "))
		}
	} else {
		fmt.Printf("Decision:    \033[31mBLOCKED\033[0m\n")
		fmt.Printf("Reason:      %s\n", decision.Reason)
		fmt.Println()
		if decision.Tier == proxy.TierAllowlist {
			fmt.Println("To allow this host, run:")
			fmt.Printf("  construct network allow %s\n", decision.TargetHost)
		}
	}
	fmt.Println()
}

// ShowLog displays egress proxy access logs and security events.
func ShowLog(args []string) {
	logPath := filepath.Join(config.GetLogsDir(), "proxy.log")

	tailCount := 50
	follow := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--follow", "-f":
			follow = true
		case "--tail", "-n":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil && n > 0 {
					tailCount = n
					i++
				}
			}
		}
	}

	file, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			ui.InfoLn("No proxy access logs found. Logs are recorded during active agent sessions.")
			return
		}
		ui.GumError(fmt.Sprintf("Failed to open proxy log: %v", err))
		return
	}
	defer file.Close() //nolint:errcheck

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		ui.GumError(fmt.Sprintf("Error reading proxy log: %v", err))
		return
	}

	if len(lines) == 0 {
		ui.InfoLn("Proxy log is empty.")
		if !follow {
			return
		}
	}

	start := 0
	if len(lines) > tailCount {
		start = len(lines) - tailCount
	}

	for _, line := range lines[start:] {
		printFormattedLogLine(line)
	}

	if !follow {
		return
	}

	for {
		time.Sleep(250 * time.Millisecond)
		for scanner.Scan() {
			printFormattedLogLine(scanner.Text())
		}
	}
}

func printFormattedLogLine(line string) {
	switch {
	case strings.Contains(line, "action=ALLOW"):
		fmt.Printf("\033[32m%s\033[0m\n", line)
	case strings.Contains(line, "action=BLOCK"):
		fmt.Printf("\033[31m%s\033[0m\n", line)
	case strings.Contains(line, "action=LEAK_DROP") || strings.Contains(line, "action=FRONTING_DROP"):
		fmt.Printf("\033[33m%s\033[0m\n", line)
	default:
		fmt.Println(line)
	}
}
