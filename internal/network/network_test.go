package network

import (
	"testing"
)

func TestValidateMode(t *testing.T) {
	validModes := []string{
		"allowlist",
		"strict",
		"open",
		"permissive",
		"internet",
		"none",
		"offline",
		"ALLOWLIST",
		"Strict",
		"  open  ",
	}

	for _, m := range validModes {
		if err := ValidateMode(m); err != nil {
			t.Errorf("expected mode %q to be valid, got error: %v", m, err)
		}
	}

	invalidModes := []string{
		"unknown",
		"any",
		"closed",
		"local",
	}

	for _, m := range invalidModes {
		if err := ValidateMode(m); err == nil {
			t.Errorf("expected mode %q to be invalid, got nil", m)
		}
	}
}
