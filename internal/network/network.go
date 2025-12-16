package network

import (
	"fmt"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/config"
)

// InjectEnv adds network configuration as environment variables
func InjectEnv(env []string, config *config.Config) []string {
	env = append(env, "NETWORK_MODE="+config.Network.Mode)

	if len(config.Network.AllowedDomains) > 0 {
		env = append(env, "NETWORK_ALLOWED_DOMAINS="+strings.Join(config.Network.AllowedDomains, ","))
	}

	if len(config.Network.AllowedIPs) > 0 {
		env = append(env, "NETWORK_ALLOWED_IPS="+strings.Join(config.Network.AllowedIPs, ","))
	}

	if len(config.Network.BlockedDomains) > 0 {
		env = append(env, "NETWORK_BLOCKED_DOMAINS="+strings.Join(config.Network.BlockedDomains, ","))
	}

	if len(config.Network.BlockedIPs) > 0 {
		env = append(env, "NETWORK_BLOCKED_IPS="+strings.Join(config.Network.BlockedIPs, ","))
	}

	return env
}

// ValidateMode checks if the provided network mode is valid
func ValidateMode(mode string) error {
	switch mode {
	case "permissive", "strict", "offline":
		return nil
	default:
		return fmt.Errorf("valid modes are: permissive, strict, offline")
	}
}
