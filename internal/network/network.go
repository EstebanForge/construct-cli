package network

import (
	"fmt"

	"github.com/EstebanForge/construct-cli/internal/config"
)

// InjectEnv adds network configuration as environment variables
func InjectEnv(env []string, cfg *config.Config) []string {
	return append(env, cfg.Network.EnvVars()...)
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
