package migration

import "testing"

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		v1       string
		v2       string
		expected int
	}{
		// v1 > v2
		{"0.4.0", "0.3.0", 1},
		{"1.0.0", "0.9.9", 1},
		{"0.3.1", "0.3.0", 1},
		{"0.4.0", "0.3.9", 1},

		// v1 < v2
		{"0.3.0", "0.4.0", -1},
		{"0.9.9", "1.0.0", -1},
		{"0.3.0", "0.3.1", -1},

		// v1 == v2
		{"0.3.0", "0.3.0", 0},
		{"1.0.0", "1.0.0", 0},
		{"0.4.0", "0.4.0", 0},

		// With 'v' prefix
		{"v0.4.0", "v0.3.0", 1},
		{"v0.3.0", "v0.4.0", -1},
		{"v0.3.0", "v0.3.0", 0},

		// Mixed prefix
		{"0.4.0", "v0.3.0", 1},
		{"v0.3.0", "0.4.0", -1},
	}

	for _, tt := range tests {
		result := compareVersions(tt.v1, tt.v2)
		if result != tt.expected {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tt.v1, tt.v2, result, tt.expected)
		}
	}
}
