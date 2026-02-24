// Package network manages allow/block rules and runtime network configuration.
package network

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/config"
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
					ui.LogWarning("Failed to resolve %s: %v", target, err)
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

	// 6. Apply to running container if exists
	containerRuntime := runtime.DetectRuntime(cfg.Runtime.Engine)
	containerName := "construct-cli"

	if runtime.IsContainerRunning(containerRuntime, containerName) {
		if err := ApplyRuleToContainer(containerRuntime, containerName,
			target, action, resolvedIPs); err != nil {
			ui.GumWarning("Could not apply to running container")
			fmt.Println("   Rule will apply on next container start")
		} else {
			ui.GumSuccess("Applied to running container immediately")
		}
	} else {
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

	// 4. Remove from running container if exists
	containerRuntime := runtime.DetectRuntime(cfg.Runtime.Engine)
	containerName := "construct-cli"

	if runtime.IsContainerRunning(containerRuntime, containerName) {
		if err := RemoveRuleFromContainer(containerRuntime, containerName, target); err != nil {
			ui.GumWarning("Could not remove from running container")
			fmt.Println("   Rule will be removed on next container start")
		} else {
			ui.GumSuccess("Removed from running container immediately")
		}
	} else {
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

	if !ui.GumAvailable() {
		ListRulesBasic(cfg)
		return
	}

	// Use Gum for beautiful table display
	fmt.Println()
	cmd := ui.GetGumCommand("style", "--border", "rounded",
		"--padding", "1 2", "--bold", "Network Configuration")
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to render header: %v\n", err)
	}
	fmt.Println()

	cmd = ui.GetGumCommand("style", "--foreground", "212",
		fmt.Sprintf("Mode: %s", cfg.Network.Mode))
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to render mode: %v\n", err)
	}
	fmt.Println()

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
		fmt.Println()
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
		fmt.Println()
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
		fmt.Println()
	}

	if len(cfg.Network.AllowedDomains) == 0 && len(cfg.Network.AllowedIPs) == 0 &&
		len(cfg.Network.BlockedDomains) == 0 && len(cfg.Network.BlockedIPs) == 0 {
		ui.GumInfo("No network rules configured")
	}
}

// ListRulesBasic lists rules without Gum (fallback)
func ListRulesBasic(cfg *config.Config) {
	fmt.Println("\n=== Network Configuration ===")
	fmt.Printf("Mode: %s\n\n", cfg.Network.Mode)

	if len(cfg.Network.AllowedDomains) > 0 {
		fmt.Println("Allowed Domains:")
		for _, domain := range cfg.Network.AllowedDomains {
			fmt.Printf("  • %s\n", domain)
		}
		fmt.Println()
	}

	if len(cfg.Network.AllowedIPs) > 0 {
		fmt.Println("Allowed IPs:")
		for _, ip := range cfg.Network.AllowedIPs {
			fmt.Printf("  • %s\n", ip)
		}
		fmt.Println()
	}

	if len(cfg.Network.BlockedDomains) > 0 || len(cfg.Network.BlockedIPs) > 0 {
		fmt.Println("Blocked:")
		for _, domain := range cfg.Network.BlockedDomains {
			fmt.Printf("  • %s\n", domain)
		}
		for _, ip := range cfg.Network.BlockedIPs {
			fmt.Printf("  • %s\n", ip)
		}
		fmt.Println()
	}

	if len(cfg.Network.AllowedDomains) == 0 && len(cfg.Network.AllowedIPs) == 0 &&
		len(cfg.Network.BlockedDomains) == 0 && len(cfg.Network.BlockedIPs) == 0 {
		fmt.Println("No network rules configured")
	}
}

// ShowStatus shows the active UFW status in the container
func ShowStatus() {
	cfg, _, err := config.Load()
	if err != nil {
		ui.GumError(fmt.Sprintf("Failed to load config: %v", err))
		os.Exit(1)
	}
	containerRuntime := runtime.DetectRuntime(cfg.Runtime.Engine)
	containerName := "construct-cli"

	if !runtime.IsContainerRunning(containerRuntime, containerName) {
		ui.GumWarning("Container is not running")
		fmt.Println("Start an agent to see active network status")
		os.Exit(1)
	}

	fmt.Println("=== Active UFW Status in Container ===")
	fmt.Println()

	var cmd *exec.Cmd
	switch containerRuntime {
	case "docker", "container":
		cmd = exec.Command("docker", "exec", containerName, "/usr/local/bin/network-filter.sh", "show_status")
	case "podman":
		cmd = exec.Command("podman", "exec", containerName, "/usr/local/bin/network-filter.sh", "show_status")
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
		fmt.Println("Canceled.")
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

	return "", fmt.Errorf("invalid target: must be IP, CIDR, or domain")
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
	}

	return nil
}

// AddRuleToConfig adds a rule to the config and saves it
func AddRuleToConfig(cfg *config.Config, target, action, ruleType string) error {
	switch action {
	case "allow":
		if ruleType == "domain" {
			cfg.Network.AllowedDomains = AppendUnique(cfg.Network.AllowedDomains, target)
		} else {
			cfg.Network.AllowedIPs = AppendUnique(cfg.Network.AllowedIPs, target)
		}
	case "block":
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

// ExecUFWCommand executes a UFW command in the container via network-filter.sh
func ExecUFWCommand(containerRuntime, containerName, command, arg string) error {
	var cmd *exec.Cmd

	switch containerRuntime {
	case "docker", "container":
		cmd = exec.Command("docker", "exec", containerName, "/usr/local/bin/network-filter.sh", command, arg)
	case "podman":
		cmd = exec.Command("podman", "exec", containerName, "/usr/local/bin/network-filter.sh", command, arg)
	default:
		return fmt.Errorf("unsupported runtime: %s", containerRuntime)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("UFW command failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}
