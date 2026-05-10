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
	if !strings.Contains(Config, "env_passthrough = [") {
		t.Error("config.toml template should include env_passthrough defaults block")
	}
	defaultPassthroughKeys := []string{
		`"GITHUB_TOKEN"`,
		`"GEMINI_API_KEY"`,
		`"OPENAI_API_KEY"`,
		`"ANTHROPIC_API_KEY"`,
		`"QWEN_API_KEY"`,
		`"MINIMAX_API_KEY"`,
		`"KIMI_API_KEY"`,
		`"ZAI_API_KEY"`,
		`"MIMO_API_KEY"`,
		`"OPENCODE_API_KEY"`,
		`"CONTEXT7_API_KEY"`,
	}
	for _, key := range defaultPassthroughKeys {
		if !strings.Contains(Config, key) {
			t.Errorf("config.toml template should include default env_passthrough key %s", key)
		}
	}
	if !strings.Contains(Config, `env_passthrough_prefixes = ["CNSTR_"]`) {
		t.Error("config.toml template should include default env_passthrough_prefixes for CNSTR_")
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
	if !strings.Contains(DockerCompose, "CONSTRUCT_HOST_UID=${CONSTRUCT_HOST_UID:-}") {
		t.Error("docker-compose.yml should pass through CONSTRUCT_HOST_UID with empty default")
	}
	if !strings.Contains(DockerCompose, "CONSTRUCT_HOST_GID=${CONSTRUCT_HOST_GID:-}") {
		t.Error("docker-compose.yml should pass through CONSTRUCT_HOST_GID with empty default")
	}
	if !strings.Contains(DockerCompose, "CONSTRUCT_USERNS_REMAP=${CONSTRUCT_USERNS_REMAP:-0}") {
		t.Error("docker-compose.yml should pass through CONSTRUCT_USERNS_REMAP with default 0")
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
		`SKIP_RECURSIVE_CHOWN=1`,
		`if [ "$SKIP_RECURSIVE_CHOWN" = "0" ]; then`,
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

// TestAgentPatchCopilotPTYWrapper guards the critical properties of the Python PTY wrapper
// installed by patch_copilot_paste_wrapper(). If any of these break, Copilot image paste
// will silently stop working with no obvious error.
func TestAgentPatchCopilotPTYWrapper(t *testing.T) {
	// Version string — idempotency guard uses this to skip reinstall.
	// Bump this when the wrapper logic changes so containers reinstall.
	if !strings.Contains(AgentPatch, "construct-copilot-wrapper-v10") {
		t.Error("agent-patch.sh: missing PTY wrapper version string 'construct-copilot-wrapper-v10'; bump version when wrapper logic changes")
	}

	// Kitty Keyboard Protocol sequences — Ghostty and other modern terminals send
	// these instead of \x16 for Ctrl+V and Cmd+V respectively.
	// Without these, paste never fires in modern terminals.
	if !strings.Contains(AgentPatch, `\x1b[118;5u`) {
		t.Error("agent-patch.sh: missing KKP Ctrl+V sequence '\\x1b[118;5u' in _PASTE_TRIGGERS; Ghostty paste will not work")
	}
	if !strings.Contains(AgentPatch, `\x1b[118;9u`) {
		t.Error("agent-patch.sh: missing KKP Cmd+V sequence '\\x1b[118;9u' in _PASTE_TRIGGERS; Ghostty paste will not work")
	}

	// Legacy Ctrl+V for non-KKP terminals.
	if !strings.Contains(AgentPatch, `\x16`) {
		t.Error("agent-patch.sh: missing legacy Ctrl+V byte '\\x16' in _PASTE_TRIGGERS")
	}

	// _handle_paste function — replaces the old inline split(b'\x16') approach.
	if !strings.Contains(AgentPatch, "_handle_paste") {
		t.Error("agent-patch.sh: missing '_handle_paste' function; paste trigger loop is broken")
	}

	// sed placeholder — injected at install time with the real copilot binary path.
	// If missing, _REAL will be the literal string and copilot will not launch.
	if !strings.Contains(AgentPatch, "__CONSTRUCT_REAL_COPILOT__") {
		t.Error("agent-patch.sh: missing sed placeholder '__CONSTRUCT_REAL_COPILOT__'; _REAL path injection broken")
	}

	// npm-global candidate — the real copilot binary found here avoids Node relative-import
	// issues that break when using readlink -f through the Homebrew symlink.
	if !strings.Contains(AgentPatch, "$HOME/.npm-global/bin/$cmd") {
		t.Error("agent-patch.sh: missing '$HOME/.npm-global/bin/$cmd' candidate; _REAL resolution broken")
	}

	// rm -f before install — must remove symlink/file before cat > to avoid writing
	// through a Homebrew symlink and corrupting the npm package binary.
	if !strings.Contains(AgentPatch, `rm -f "$wrapper_path"`) {
		t.Error("agent-patch.sh: missing 'rm -f $wrapper_path' before wrapper install; stale wrapper can break launch")
	}

	// Stale copilot-real cleanup — removes broken copies left by previous wrapper versions
	// that used cp-through-symlink approach.
	if !strings.Contains(AgentPatch, `rm -f "${wrapper_path}-real"`) {
		t.Error("agent-patch.sh: missing stale 'copilot-real' cleanup; old broken copies will persist in named volume")
	}

	// PATH-priority install location — wrapper must be at the path command -v copilot resolves
	// to (Homebrew bin), not at ~/.local/bin which is shadowed by Homebrew.
	if !strings.Contains(AgentPatch, "command -v copilot") {
		t.Error("agent-patch.sh: missing 'command -v copilot' for PATH-priority install location detection")
	}

	// Python shebang — must use absolute path; /usr/bin/env python3 may not find the
	// Homebrew python3 in the restricted entrypoint environment.
	if !strings.Contains(AgentPatch, "#!/home/linuxbrew/.linuxbrew/bin/python3") {
		t.Error("agent-patch.sh: PTY wrapper shebang must use absolute Homebrew python3 path")
	}

	// Always-on log path — must write to home dir bind-mount, not /tmp.
	// /tmp is ephemeral (--rm containers); home dir persists to host.
	if !strings.Contains(AgentPatch, "construct-cli/logs") {
		t.Error("agent-patch.sh: wrapper log must use ~/.config/construct-cli/logs (host bind-mount), not /tmp")
	}
}

func TestAgentPatchCodexPTYWrapper(t *testing.T) {
	if !strings.Contains(AgentPatch, "construct-codex-wrapper-v2") {
		t.Error("agent-patch.sh: missing Codex PTY wrapper version string 'construct-codex-wrapper-v2'")
	}
	if !strings.Contains(AgentPatch, "__CONSTRUCT_REAL_CODEX__") {
		t.Error("agent-patch.sh: missing '__CONSTRUCT_REAL_CODEX__' placeholder for real codex binary injection")
	}
	if !strings.Contains(AgentPatch, "command -v codex") {
		t.Error("agent-patch.sh: missing 'command -v codex' detection for wrapper install location")
	}
	if !strings.Contains(AgentPatch, "$HOME/.npm-global/bin/$cmd") {
		t.Error("agent-patch.sh: missing npm-global codex candidate resolution")
	}
	if !strings.Contains(AgentPatch, "construct-codex-wrapper.log") {
		t.Error("agent-patch.sh: codex wrapper log path missing")
	}
	// Codex expects raw file path paste, not @path.
	if !strings.Contains(AgentPatch, "out += f'{img} '.encode()") {
		t.Error("agent-patch.sh: codex wrapper should inject raw image path without '@' prefix")
	}
}

// TestAgentPatchCopilotJSBridge guards the JS bridge version string and key exports.
// The bridge replaces @teddyzhu/clipboard's NAPI-RS native addon with a pure-JS HTTP client.
func TestAgentPatchCopilotJSBridge(t *testing.T) {
	if !strings.Contains(AgentPatch, "construct-copilot-clipboard-bridge-v3") {
		t.Error("agent-patch.sh: missing JS bridge version string 'construct-copilot-clipboard-bridge-v3'")
	}

	// Core API surface that Copilot and Qwen depend on.
	for _, export := range []string{"getClipboardImageData", "getClipboardFiles", "ClipboardManager"} {
		if !strings.Contains(AgentPatch, export) {
			t.Errorf("agent-patch.sh: JS bridge missing export %q", export)
		}
	}
}

// TestAgentPatchCopilotKeybinding guards the Ink TUI keybinding patch.
func TestAgentPatchCopilotKeybinding(t *testing.T) {
	if !strings.Contains(AgentPatch, "construct-copilot-keybinding-v1") {
		t.Error("agent-patch.sh: missing keybinding patch version string 'construct-copilot-keybinding-v1'")
	}
	// Must accept ctrl on Linux (fe.ctrl||fe.meta), not only fe.meta.
	if !strings.Contains(AgentPatch, "fe.ctrl||fe.meta") {
		t.Error("agent-patch.sh: keybinding patch must allow Ctrl+V (fe.ctrl||fe.meta) on Linux")
	}
}

// TestClipperShimFilePathMode guards that the clipper shim emits @path for file-paste agents
// and raw bytes otherwise. This is the routing decision point for all non-Copilot agents.
func TestClipperShimFilePathMode(t *testing.T) {
	// File-path agents receive @path reference.
	if !strings.Contains(Clipper, "CONSTRUCT_FILE_PASTE_AGENTS") {
		t.Error("clipper: missing CONSTRUCT_FILE_PASTE_AGENTS check for mode routing")
	}
	if !strings.Contains(Clipper, "PATH_AGENT") {
		t.Error("clipper: missing PATH_AGENT variable for file-path vs raw-bytes decision")
	}
	// Must save to .construct-clipboard/ directory.
	if !strings.Contains(Clipper, ".construct-clipboard") {
		t.Error("clipper: missing '.construct-clipboard' save directory")
	}
	// Auth header must match server expectation.
	if !strings.Contains(Clipper, "X-Construct-Clip-Token") {
		t.Error("clipper: missing 'X-Construct-Clip-Token' auth header in curl call")
	}
	// Image resize guard — prevents OOM on huge screenshots.
	if !strings.Contains(Clipper, "8000") {
		t.Error("clipper: missing image size limit (8000px) before raw-bytes emit")
	}
}

func TestEntrypointUsernsRemapSkipsRecursiveChown(t *testing.T) {
	guard := `if [ "$SKIP_RECURSIVE_CHOWN" = "0" ]; then`
	chownHome := `chown -R "$RUN_AS_CHOWN" /home/construct`
	chownBrew := `chown -R "$RUN_AS_CHOWN" /home/linuxbrew/.linuxbrew`

	guardIdx := strings.Index(Entrypoint, guard)
	if guardIdx == -1 {
		t.Fatalf("entrypoint regression: missing recursive chown guard: %s", guard)
	}

	chownHomeIdx := strings.Index(Entrypoint, chownHome)
	if chownHomeIdx == -1 {
		t.Fatalf("entrypoint regression: missing home recursive chown: %s", chownHome)
	}
	chownBrewIdx := strings.Index(Entrypoint, chownBrew)
	if chownBrewIdx == -1 {
		t.Fatalf("entrypoint regression: missing brew recursive chown: %s", chownBrew)
	}

	if chownHomeIdx < guardIdx || chownBrewIdx < guardIdx {
		t.Fatalf("entrypoint regression: recursive chown must be gated by SKIP_RECURSIVE_CHOWN")
	}
}
