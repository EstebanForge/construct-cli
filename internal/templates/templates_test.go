package templates

import (
	"strings"
	"testing"
)

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
	if !strings.Contains(Dockerfile, "FROM debian:trixie-slim") {
		t.Error("Dockerfile template missing base image declaration")
	}
	if !strings.Contains(Dockerfile, "brew install") {
		t.Error("Dockerfile template missing Homebrew installation")
	}
	if !strings.Contains(Dockerfile, "WORKDIR /projects") {
		t.Error("Dockerfile template missing WORKDIR /projects")
	}

	// Test that docker-compose.yml contains expected content
	if !strings.Contains(DockerCompose, "services:") {
		t.Error("docker-compose.yml template missing services section")
	}
	if !strings.Contains(DockerCompose, "construct-box") {
		t.Error("docker-compose.yml template missing service name")
	}
	if !strings.Contains(DockerCompose, "${CONSTRUCT_PROJECT_PATH:-/workspace}") {
		t.Error("docker-compose.yml template missing dynamic project path")
	}

	// Test that config.toml contains expected sections
	if !strings.Contains(Config, "[runtime]") {
		t.Error("config.toml template missing [runtime] section")
	}
	if !strings.Contains(Config, "[network]") {
		t.Error("config.toml template missing [network] section")
	}
	if !strings.Contains(Config, "[sandbox]") {
		t.Error("config.toml template missing [sandbox] section")
	}
	if !strings.Contains(Config, "[agents]") {
		t.Error("config.toml template missing [agents] section")
	}

	// Test Clipper template
	if Clipper == "" {
		t.Error("clipper template is empty")
	}
	if !strings.Contains(Clipper, "#!/bin/bash") {
		t.Error("clipper template missing shebang")
	}

	// Test Osascript template
	if Osascript == "" {
		t.Error("osascript template is empty")
	}
	if !strings.Contains(Osascript, "/tmp/osascript_debug.log") {
		t.Error("osascript template missing /tmp log path")
	}

	// Test clipboard sync template
	if ClipboardX11Sync == "" {
		t.Error("clipboard-x11-sync template is empty")
	}
	if !strings.Contains(ClipboardX11Sync, "#!/usr/bin/env bash") {
		t.Error("clipboard-x11-sync template missing shebang")
	}

	// Test UpdateAll template
	if UpdateAll == "" {
		t.Error("update-all.sh template is empty")
	}
	if !strings.Contains(UpdateAll, "#!/usr/bin/env bash") {
		t.Error("update-all.sh template missing shebang")
	}
}

func TestUpdateAllSudoDetection(t *testing.T) {
	// Verify sudo detection is present (prevents OrbStack PAM issues)
	if !strings.Contains(UpdateAll, "sudo -n true 2>/dev/null") {
		t.Error("update-all.sh should test if sudo works non-interactively")
	}
	if !strings.Contains(UpdateAll, "SUDO=\"\"") {
		t.Error("update-all.sh should handle case when sudo unavailable")
	}
	// Verify no hardcoded 'sudo apt-get' in the fallback section
	if strings.Contains(UpdateAll, "sudo apt-get") {
		t.Error("update-all.sh should not contain hardcoded 'sudo apt-get', should use '$SUDO apt-get'")
	}
	// Verify $SUDO variable is used
	if !strings.Contains(UpdateAll, "$SUDO apt-get") {
		t.Error("update-all.sh should use '$SUDO apt-get' for privileged operations")
	}
}
