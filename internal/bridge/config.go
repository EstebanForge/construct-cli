// Package bridge provides host service bridging for construct-cli sandboxes.
// It allows containers to access services running on the host machine.
package bridge

import (
	"fmt"
	"strings"
)

// HostServiceConfig represents configuration for host service bridging.
type HostServiceConfig struct {
	Enabled      bool     `toml:"enabled"`        // Enable host service bridge
	AutoDetect   bool     `toml:"auto_detect"`    // Automatically detect host gateway
	OnFailure    string   `toml:"on_failure"`     // Behavior on detection failure: "warn", "fail", "silent"
	ManualHostIP string   `toml:"manual_host_ip"` // Manual override for host IP
	Services     []string `toml:"services"`       // Services to bridge: ["name:port"]
}

// DefaultHostServiceConfig returns default host service configuration.
func DefaultHostServiceConfig() HostServiceConfig {
	return HostServiceConfig{
		Enabled:    false,
		AutoDetect: true,
		OnFailure:  "warn",
		Services:   []string{},
	}
}

// Validate validates the host service configuration.
func (c *HostServiceConfig) Validate() error {
	if !c.Enabled {
		return nil
	}

	// Validate on_failure value
	switch c.OnFailure {
	case "warn", "fail", "silent":
		// Valid values
	default:
		return fmt.Errorf("invalid on_failure value: %s (must be 'warn', 'fail', or 'silent')", c.OnFailure)
	}

	// Validate services format
	for _, service := range c.Services {
		if !strings.Contains(service, ":") {
			return fmt.Errorf("invalid service format: %s (must be 'name:port')", service)
		}
		parts := strings.SplitN(service, ":", 2)
		if parts[0] == "" {
			return fmt.Errorf("service name cannot be empty in: %s", service)
		}
		if parts[1] == "" {
			return fmt.Errorf("service port cannot be empty in: %s", service)
		}
	}

	return nil
}

// GetServiceEnv generates environment variables for configured services.
func (c *HostServiceConfig) GetServiceEnv(hostIP string) map[string]string {
	if !c.Enabled || hostIP == "" {
		return nil
	}

	env := make(map[string]string)
	env["CONSTRUCT_HOST_IP"] = hostIP

	for _, service := range c.Services {
		parts := strings.SplitN(service, ":", 2)
		name := strings.ToUpper(parts[0])
		port := parts[1]

		// Generate service-specific URLs
		env[fmt.Sprintf("CONSTRUCT_%s_HOST", name)] = hostIP
		env[fmt.Sprintf("CONSTRUCT_%s_PORT", name)] = port
		env[fmt.Sprintf("CONSTRUCT_%s_URL", name)] = fmt.Sprintf("http://%s:%s", hostIP, port)
	}

	return env
}

// GetServiceNames returns a list of configured service names.
func (c *HostServiceConfig) GetServiceNames() []string {
	if !c.Enabled {
		return nil
	}

	names := make([]string, 0, len(c.Services))
	for _, service := range c.Services {
		parts := strings.SplitN(service, ":", 2)
		names = append(names, parts[0])
	}
	return names
}
