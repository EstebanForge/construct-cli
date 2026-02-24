package semver

import "testing"

func TestCompare(t *testing.T) {
	tests := []struct {
		name     string
		left     string
		right    string
		expected int
	}{
		{name: "major greater", left: "2.0.0", right: "1.9.9", expected: 1},
		{name: "minor lower", left: "1.4.0", right: "1.5.0", expected: -1},
		{name: "patch equal", left: "1.5.0", right: "1.5.0", expected: 0},
		{name: "release greater than prerelease", left: "1.5.0", right: "1.5.0-beta.1", expected: 1},
		{name: "prerelease lower than release", left: "1.5.0-beta.1", right: "1.5.0", expected: -1},
		{name: "prerelease numeric compare", left: "1.5.0-beta.10", right: "1.5.0-beta.2", expected: 1},
		{name: "prerelease lexical compare", left: "1.5.0-beta.b", right: "1.5.0-beta.a", expected: 1},
		{name: "prerelease numeric lower than lexical", left: "1.5.0-beta.1", right: "1.5.0-beta.alpha", expected: -1},
		{name: "prerelease length tie-break", left: "1.5.0-beta", right: "1.5.0-beta.1", expected: -1},
		{name: "ignore build metadata", left: "1.5.0+build.1", right: "1.5.0+build.9", expected: 0},
		{name: "supports v prefix", left: "v1.5.1", right: "1.5.0", expected: 1},
		{name: "invalid numeric segment defaults to zero", left: "1.5.a", right: "1.5.0", expected: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Compare(tt.left, tt.right)
			if result != tt.expected {
				t.Fatalf("Compare(%q, %q) = %d, want %d", tt.left, tt.right, result, tt.expected)
			}
		})
	}
}
