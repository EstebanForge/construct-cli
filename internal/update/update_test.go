package update

import "testing"

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		v1       string
		v2       string
		expected int
	}{
		// v1 > v2
		{"0.10.1", "0.10.0", 1},
		{"1.0.0", "0.9.9", 1},
		{"0.3.1", "0.3.0", 1},

		// v1 < v2
		{"0.10.0", "0.10.1", -1},
		{"0.9.9", "1.0.0", -1},

		// v1 == v2
		{"0.10.1", "0.10.1", 0},
		{"1.0.0", "1.0.0", 0},

		// Handle potential non-numeric segments gracefully (should default to 0)
		{"0.10.a", "0.10.0", 0},
		{"0.10", "0.10.0", 0},
	}

	for _, tt := range tests {
		result := compareVersions(tt.v1, tt.v2)
		if result != tt.expected {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tt.v1, tt.v2, result, tt.expected)
		}
	}
}
