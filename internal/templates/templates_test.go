package templates

import "testing"

// TestEmbeddedTemplates tests that embedded templates are not empty
func TestEmbeddedTemplates(t *testing.T) {
	if Dockerfile == "" {
		t.Error("Dockerfile template is empty")
	}
	if DockerCompose == "" {
		t.Error("docker-compose.yml template is empty")
	}
	if Config == "" {
		t.Error("config.toml template is empty")
	}

	// Test that Dockerfile contains expected content
	if !containsString(Dockerfile, "FROM debian:trixie-slim") {
		t.Error("Dockerfile template missing base image declaration")
	}
	if !containsString(Dockerfile, "brew install") {
		t.Error("Dockerfile template missing Homebrew installation")
	}
	if !containsString(Dockerfile, "WORKDIR /workspace") {
		t.Error("Dockerfile template missing WORKDIR /workspace")
	}

	// Test that docker-compose.yml contains expected content
	if !containsString(DockerCompose, "services:") {
		t.Error("docker-compose.yml template missing services section")
	}
	if !containsString(DockerCompose, "construct-box") {
		t.Error("docker-compose.yml template missing service name")
	}

	// Test that config.toml contains expected sections
	if !containsString(Config, "[runtime]") {
		t.Error("config.toml template missing [runtime] section")
	}
	if !containsString(Config, "[network]") {
		t.Error("config.toml template missing [network] section")
	}
	if !containsString(Config, "[sandbox]") {
		t.Error("config.toml template missing [sandbox] section")
	}

	// Test Clipper template
	if Clipper == "" {
		t.Error("clipper template is empty")
	}
	if !containsString(Clipper, "#!/bin/bash") {
		t.Error("clipper template missing shebang")
	}
}

// Helper function to check if string contains substring
func containsString(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 &&
		(s == substr || len(s) >= len(substr) &&
			func() bool {
				for i := 0; i <= len(s)-len(substr); i++ {
					if s[i:i+len(substr)] == substr {
						return true
					}
				}
				return false
			}())
}
