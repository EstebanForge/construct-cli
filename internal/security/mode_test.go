package security

import (
	"testing"
)

func TestResolveEnablement(t *testing.T) {
	tests := []struct {
		name           string
		configEnabled  bool
		expectedMode   Mode
		expectedSource string
	}{
		{
			name:           "config disabled",
			configEnabled:  false,
			expectedMode:   ModeDisabled,
			expectedSource: "config_disabled",
		},
		{
			name:           "config enabled",
			configEnabled:  true,
			expectedMode:   ModeEnabled,
			expectedSource: "config_enabled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := ResolveEnablement(tt.configEnabled)

			if status.Mode != tt.expectedMode {
				t.Errorf("expected mode %d, got %d", tt.expectedMode, status.Mode)
			}

			if status.Source != tt.expectedSource {
				t.Errorf("expected source %q, got %q", tt.expectedSource, status.Source)
			}

			if status.IsEnabled() != (tt.expectedMode == ModeEnabled) {
				t.Errorf("expected IsEnabled %v, got %v", tt.expectedMode == ModeEnabled, status.IsEnabled())
			}
		})
	}
}
