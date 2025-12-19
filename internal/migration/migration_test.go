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

func TestDeepMerge(t *testing.T) {
	base := map[string]interface{}{
		"string": "base_value",
		"number": 42,
		"nested": map[string]interface{}{
			"keep":    "base",
			"replace": "base",
		},
		"only_in_base": "value",
	}

	override := map[string]interface{}{
		"string": "override_value",
		"nested": map[string]interface{}{
			"replace": "override",
			"new":     "value",
		},
		"only_in_override": "value",
	}

	result := deepMerge(base, override)

	// Check overridden values
	if result["string"] != "override_value" {
		t.Errorf("Expected string to be overridden, got %v", result["string"])
	}

	// Check preserved value from base
	if result["number"] != 42 {
		t.Errorf("Expected number to be preserved, got %v", result["number"])
	}

	// Check nested merge
	nested := result["nested"].(map[string]interface{})
	if nested["keep"] != "base" {
		t.Errorf("Expected nested.keep to be preserved from base, got %v", nested["keep"])
	}
	if nested["replace"] != "override" {
		t.Errorf("Expected nested.replace to be overridden, got %v", nested["replace"])
	}
	if nested["new"] != "value" {
		t.Errorf("Expected nested.new to be added from override, got %v", nested["new"])
	}

	// Check values from both maps are present
	if result["only_in_base"] != "value" {
		t.Errorf("Expected only_in_base to be present, got %v", result["only_in_base"])
	}
	if result["only_in_override"] != "value" {
		t.Errorf("Expected only_in_override to be present, got %v", result["only_in_override"])
	}
}
