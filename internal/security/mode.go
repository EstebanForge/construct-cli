// Package security implements secret redaction and security boundaries for Construct.
package security

import (
	"fmt"
	"os"
)

const (
	// ExperimentGateEnv is the master environment variable that gates the entire hide-secrets feature.
	// Must be set to "1" for the feature to be available.
	ExperimentGateEnv = "CONSTRUCT_EXPERIMENT_HIDE_SECRETS"
)

// Mode represents the effective hide-secrets mode state.
type Mode int

const (
	// ModeDisabled indicates hide-secrets is not active.
	ModeDisabled Mode = iota
	// ModeEnabled indicates hide-secrets is active and all protections are enforced.
	ModeEnabled
)

// EnablementStatus describes why hide-secrets is in a particular state.
type EnablementStatus struct {
	Mode         Mode   `json:"mode"`
	Reason       string `json:"reason"`        // Human-readable explanation
	Source       string `json:"source"`        // "experiment_gate_disabled", "config_disabled", "config_enabled"
	FeatureGated bool   `json:"feature_gated"` // True if CONSTRUCT_EXPERIMENT_HIDE_SECRETS != "1"
}

// IsEnabled returns true if hide-secrets mode is effectively enabled.
func (s *EnablementStatus) IsEnabled() bool {
	return s.Mode == ModeEnabled
}

// ResolveEnablement determines whether hide-secrets mode should be active based on
// the experiment gate and user config.
func ResolveEnablement(configEnabled bool) *EnablementStatus {
	// Check master experiment gate first
	gateValue := os.Getenv(ExperimentGateEnv)
	featureGated := gateValue != "1"

	if featureGated {
		// Feature is gated off - config is ignored
		return &EnablementStatus{
			Mode:         ModeDisabled,
			Reason:       fmt.Sprintf("%s env var is not set to '1'", ExperimentGateEnv),
			Source:       "experiment_gate_disabled",
			FeatureGated: true,
		}
	}

	// Gate is open, check user config
	if !configEnabled {
		return &EnablementStatus{
			Mode:         ModeDisabled,
			Reason:       "security.hide_secrets is set to false in config",
			Source:       "config_disabled",
			FeatureGated: false,
		}
	}

	// Both gates passed
	return &EnablementStatus{
		Mode:         ModeEnabled,
		Reason:       "Enabled via config with experiment gate satisfied",
		Source:       "config_enabled",
		FeatureGated: false,
	}
}

// LogStartupStatus emits a startup log line indicating hide-secrets state.
func LogStartupStatus(status *EnablementStatus) {
	switch status.Source {
	case "config_disabled":
		fmt.Printf("hide_secrets=off (source=config)\n")
	case "config_enabled":
		fmt.Printf("hide_secrets=on (source=config)\n")
	}
	// Note: experiment_gate_disabled produces no output to avoid bothering users
}
