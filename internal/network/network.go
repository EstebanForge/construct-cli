package network

import (
	"github.com/EstebanForge/construct-cli/internal/config"
)

// InjectEnv adds network configuration as environment variables
func InjectEnv(env []string, cfg *config.Config) []string {
	return append(env, cfg.Network.EnvVars()...)
}

// NormalizeMode validates and canonicalizes the network mode string.
func NormalizeMode(mode string) (string, error) {
	return config.NormalizeNetworkMode(mode)
}

// ValidateMode checks if the provided network mode is valid.
func ValidateMode(mode string) error {
	_, err := config.NormalizeNetworkMode(mode)
	return err
}
