package security

import (
	"testing"
)

func TestMatchesCandidatePattern(t *testing.T) {
	tests := []struct {
		name      string
		relPath   string
		denyPaths []string
		expected  bool
	}{
		{"dotenv file", ".env", nil, true},
		{"dotenv local", ".env.local", nil, true},
		{"pem file", "cert.pem", nil, true},
		{"private key", "id_rsa", nil, true},
		{"config yaml", "config.yaml", nil, true},
		{"config json", "config.json", nil, true},
		{"tfvars", "terraform.tfvars", nil, true},
		{"regular file", "main.go", nil, false},
		{"custom deny path", "secrets.yml", []string{"secrets.yml"}, true},
		{"custom wildcard", "custom.*.json", []string{"custom.*.json"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MatchesCandidatePattern(tt.relPath, tt.denyPaths)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestIsHardExcluded(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		projectRoot string
		expected    bool
	}{
		{"git directory", "/project/.git", "/project", true},
		{"node_modules", "/project/node_modules", "/project", true},
		{"venv", "/project/.venv", "/project", true},
		{"vendor", "/project/vendor", "/project", true},
		{"nested git", "/project/subdir/.git", "/project", true},
		{"regular dir", "/project/src", "/project", false},
		{"regular file", "/project/main.go", "/project", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsHardExcluded(tt.path, tt.projectRoot)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}
