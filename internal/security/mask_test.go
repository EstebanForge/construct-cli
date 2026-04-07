package security

import (
	"testing"
)

func TestMaskerHash(t *testing.T) {
	m := NewMasker("hash")
	secret := "my-secret-value-123"

	mask1 := m.Mask(secret)
	mask2 := m.Mask(secret)

	// Same secret should produce same mask
	if mask1 != mask2 {
		t.Errorf("same secret should produce same mask: %s != %s", mask1, mask2)
	}

	// Mask should not contain raw secret characters
	if contains(mask1, "my-secret-value-123") {
		t.Error("mask should not contain raw secret characters")
	}

	// Mask should have correct prefix
	if !startsWith(mask1, MaskPrefix) {
		t.Errorf("mask should start with %s", MaskPrefix)
	}
}

func TestMaskerFixed(t *testing.T) {
	m := NewMasker("fixed")
	secret1 := "secret-one"
	secret2 := "secret-two"

	mask1 := m.Mask(secret1)
	mask2 := m.Mask(secret2)

	// All secrets should produce the same mask
	if mask1 != mask2 {
		t.Errorf("fixed style should produce same mask for all secrets: %s != %s", mask1, mask2)
	}

	// Should be the fixed placeholder
	if mask1 != FixedMask {
		t.Errorf("expected %s, got %s", FixedMask, mask1)
	}
}

func TestIsMaskedValue(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected bool
	}{
		{"fixed mask", FixedMask, true},
		{"hash mask", "CONSTRUCT_REDACTED_A1B2C3D4", true},
		{"hash mask lowercase", "CONSTRUCT_REDACTED_a1b2c3d4", false},
		{"hash mask wrong length", "CONSTRUCT_REDACTED_A1B2", false},
		{"regular string", "my-secret-value", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsMaskedValue(tt.value)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && indexOf(s, substr) >= 0)
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
