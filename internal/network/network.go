package network

import (
	"fmt"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/config"
)

// InjectEnv adds network configuration as environment variables
func InjectEnv(env []string, cfg *config.Config) []string {
	return append(env, cfg.Network.EnvVars()...)
}

// ValidateMode checks if the provided network mode is valid
func ValidateMode(mode string) error {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "permissive", "strict", "offline", "open", "allowlist", "internet", "none":
		return nil
	default:
		return fmt.Errorf("valid modes are: allowlist (strict), open (permissive), internet, none (offline)")
	}
}
