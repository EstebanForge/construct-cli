package bridge

import (
	"fmt"
	"os"

	"github.com/EstebanForge/construct-cli/internal/ui"
)

// InjectHostBridgeConfig adds host service bridge configuration to docker-compose override.
// This function should be called during docker-compose.override.yml generation.
func InjectHostBridgeConfig(overrideBuilder *OverrideBuilder, cfg *HostServiceConfig, containerRuntime string) {
	if !cfg.Enabled {
		return
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		ui.GumError(fmt.Sprintf("Invalid host bridge configuration: %v", err))
		os.Exit(1)
	}

	// Check for manual host IP override
	hostIP := ""
	if cfg.ManualHostIP != "" {
		hostIP = cfg.ManualHostIP
		ui.LogDebug("Using manual host IP: %s", hostIP)
	} else if cfg.AutoDetect {
		// Auto-detect host gateway
		result := DetectHostGateway(containerRuntime, cfg.OnFailure)
		if result.Success {
			hostIP = result.HostIP
			ui.LogDebug("Detected host IP: %s via %s", hostIP, result.Method)
		}
		// If detection failed and on_failure is "warn" or "silent", continue without host access
		// If on_failure is "fail", DetectHostGateway already exited
	}

	// Only add extra_hosts if we have a valid host IP
	if hostIP != "" {
		overrideBuilder.AddExtraHosts(hostIP)

		// Log enabled services
		services := cfg.GetServiceNames()
		if len(services) > 0 {
			ui.LogDebug("Host bridge enabled for services: %v", services)
		}
	}
}

// GetBridgeEnvironment returns environment variables for host services.
func GetBridgeEnvironment(cfg *HostServiceConfig, containerRuntime string) map[string]string {
	if !cfg.Enabled {
		return nil
	}

	// Get host IP (similar logic to InjectHostBridgeConfig)
	hostIP := ""
	if cfg.ManualHostIP != "" {
		hostIP = cfg.ManualHostIP
	} else if cfg.AutoDetect {
		result := DetectHostGateway(containerRuntime, cfg.OnFailure)
		if result.Success {
			hostIP = result.HostIP
		}
	}

	if hostIP == "" {
		return nil
	}

	return cfg.GetServiceEnv(hostIP)
}

// OverrideBuilder helps build docker-compose override content.
type OverrideBuilder struct {
	content string
}

// NewOverrideBuilder creates a new override builder.
func NewOverrideBuilder() *OverrideBuilder {
	return &OverrideBuilder{content: ""}
}

// AddExtraHosts adds extra_hosts configuration.
func (b *OverrideBuilder) AddExtraHosts(hostIP string) {
	b.content += "    extra_hosts:\n"
	b.content += fmt.Sprintf("      - \"host.docker.internal:%s\"\n", hostIP)
}

// GetContent returns the accumulated content.
func (b *OverrideBuilder) GetContent() string {
	return b.content
}

// HasContent returns true if any content has been added.
func (b *OverrideBuilder) HasContent() bool {
	return b.content != ""
}
