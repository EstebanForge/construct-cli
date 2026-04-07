package security

import (
	"os"
	"testing"
)

func TestResolveEnablement(t *testing.T) {
	tests := []struct {
		name           string
		envGateValue   string
		configEnabled  bool
		expectedMode   Mode
		expectedSource string
	}{
		{
			name:           "experiment gate disabled",
			envGateValue:   "",
			configEnabled:  true,
			expectedMode:   ModeDisabled,
			expectedSource: "experiment_gate_disabled",
		},
		{
			name:           "experiment gate wrong value",
			envGateValue:   "0",
			configEnabled:  true,
			expectedMode:   ModeDisabled,
			expectedSource: "experiment_gate_disabled",
		},
		{
			name:           "gate open but config disabled",
			envGateValue:   "1",
			configEnabled:  false,
			expectedMode:   ModeDisabled,
			expectedSource: "config_disabled",
		},
		{
			name:           "both gates enabled",
			envGateValue:   "1",
			configEnabled:  true,
			expectedMode:   ModeEnabled,
			expectedSource: "config_enabled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set env var
			if tt.envGateValue != "" {
				os.Setenv(ExperimentGateEnv, tt.envGateValue)
				defer os.Unsetenv(ExperimentGateEnv)
			}

			status := ResolveEnablement(tt.configEnabled)

			if status.Mode != tt.expectedMode {
				t.Errorf("expected mode %d, got %d", tt.expectedMode, status.Mode)
			}

			if status.Source != tt.expectedSource {
				t.Errorf("expected source %s, got %s", tt.expectedSource, status.Source)
			}

			if status.IsEnabled() && tt.expectedMode != ModeEnabled {
				t.Error("expected IsEnabled to be false")
			}
		})
	}
}
