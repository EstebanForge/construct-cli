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
	if !strings.Contains(DockerCompose, "${CONSTRUCT_PROJECT_PATH}") {
		t.Error("docker-compose.yml template missing dynamic project path")
	}
	if !strings.Contains(DockerCompose, "- CGO_ENABLED=1") {
		t.Error("docker-compose.yml template missing CGO_ENABLED=1")
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
	if !strings.Contains(Config, "non_root_strict = false") {
		t.Error("config.toml template should include non_root_strict with default false")
	}
	if !strings.Contains(Config, "allow_custom_compose_override = false") {
		t.Error("config.toml template should include allow_custom_compose_override with default false")
	}
	if !strings.Contains(Config, "exec_as_host_user = true") {
		t.Error("config.toml template should include exec_as_host_user with default true")
	}
	if !strings.Contains(Config, "update_channel = \"stable\"") {
		t.Error("config.toml template should include update_channel with default stable")
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
	if !strings.Contains(UpdateAll, "Construct update diagnostics") {
		t.Error("update-all.sh should include diagnostics header")
	}
	if !strings.Contains(UpdateAll, "Post-update command verification") {
		t.Error("update-all.sh should include post-update command verification")
	}
	if !strings.Contains(UpdateAll, "Missing commands after update") {
		t.Error("update-all.sh should report missing commands after verification")
	}
	if !strings.Contains(UpdateAll, "npm config set prefix \"$HOME/.npm-global\"") {
		t.Error("update-all.sh should configure npm global prefix before npm updates")
	}

	// Test agent patch template
	if AgentPatch == "" {
		t.Error("agent-patch.sh template is empty")
	}
	if !strings.Contains(AgentPatch, "#!/usr/bin/env bash") {
		t.Error("agent-patch.sh template missing shebang")
	}

	// Test entrypoint hash template
	if EntrypointHash == "" {
		t.Error("entrypoint-hash.sh template is empty")
	}
	if !strings.Contains(EntrypointHash, "#!/usr/bin/env bash") {
		t.Error("entrypoint-hash.sh template missing shebang")
	}

	// Verify entrypoint uses shared hash and patch scripts
	if !strings.Contains(Entrypoint, "entrypoint-hash.sh") {
		t.Error("entrypoint.sh should source entrypoint-hash.sh")
	}
	if !strings.Contains(Entrypoint, "agent-patch.sh") {
		t.Error("entrypoint.sh should call agent-patch.sh")
	}
	if !strings.Contains(Entrypoint, "touch \"$HOME/.bashrc\"") {
		t.Error("entrypoint.sh should create .bashrc before grep checks")
	}
	if !strings.Contains(Entrypoint, "RUN_AS_USER=\"construct\"") {
		t.Error("entrypoint.sh should define construct user as default runtime user")
	}
	if !strings.Contains(Entrypoint, "RUN_AS_USER=\"${CONSTRUCT_HOST_UID}:${CONSTRUCT_HOST_GID}\"") {
		t.Error("entrypoint.sh should support host uid:gid runtime mapping when provided")
	}
	if !strings.Contains(Entrypoint, "export HOME=\"${HOME:-/home/construct}\"") {
		t.Error("entrypoint.sh should preserve HOME before privilege drop")
	}
	if !strings.Contains(DockerCompose, "CONSTRUCT_HOST_UID=${CONSTRUCT_HOST_UID}") {
		t.Error("docker-compose.yml should pass through CONSTRUCT_HOST_UID")
	}
	if !strings.Contains(DockerCompose, "CONSTRUCT_HOST_GID=${CONSTRUCT_HOST_GID}") {
		t.Error("docker-compose.yml should pass through CONSTRUCT_HOST_GID")
	}
	if !strings.Contains(DockerCompose, "CONSTRUCT_USERNS_REMAP=${CONSTRUCT_USERNS_REMAP}") {
		t.Error("docker-compose.yml should pass through CONSTRUCT_USERNS_REMAP")
	}

	// Verify update-all uses shared hash helper
	if !strings.Contains(UpdateAll, "entrypoint-hash.sh") {
		t.Error("update-all.sh should source entrypoint-hash.sh")
	}
	if !strings.Contains(UpdateAll, "write_entrypoint_hash") {
		t.Error("update-all.sh should write entrypoint hash via helper")
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

func TestEntrypointPrivilegeDropRegression(t *testing.T) {
	// Regression guard:
	// keep construct as default runtime user but allow Linux host uid/gid mapping
	// to prevent mounted home ownership drift.
	requiredFragments := []string{
		`RUN_AS_USER="construct"`,
		`RUN_AS_CHOWN="construct:construct"`,
		`if [[ "${CONSTRUCT_HOST_UID:-}" =~ ^[0-9]+$ ]] && [[ "${CONSTRUCT_HOST_GID:-}" =~ ^[0-9]+$ ]]; then`,
		`RUN_AS_USER="${CONSTRUCT_HOST_UID}:${CONSTRUCT_HOST_GID}"`,
		`RUN_AS_CHOWN="${CONSTRUCT_HOST_UID}:${CONSTRUCT_HOST_GID}"`,
		`if [ "$(id -u)" = "0" ] && [ "${CONSTRUCT_USERNS_REMAP:-0}" = "1" ]; then`,
		`RUN_AS_USER="root"`,
		`RUN_AS_CHOWN="0:0"`,
		`export HOME="${HOME:-/home/construct}"`,
		`exec gosu "$RUN_AS_USER" "$0" "$@"`,
		`chown -R "$RUN_AS_CHOWN" /home/construct`,
		`construct_profile="$HOME/.construct-path.sh"`,
		`local ssh_dir="$HOME/.ssh"`,
	}

	for _, fragment := range requiredFragments {
		if !strings.Contains(Entrypoint, fragment) {
			t.Fatalf("entrypoint regression: missing required fragment: %s", fragment)
		}
	}
}
