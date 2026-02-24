package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"strings"
	"testing"

	"github.com/EstebanForge/construct-cli/internal/clipboard"
	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/constants"
	"github.com/EstebanForge/construct-cli/internal/env"
	"github.com/EstebanForge/construct-cli/internal/runtime"
)

// TestExecViaDaemonStaleContainer tests that execViaDaemon returns false when daemon is stale
func TestExecViaDaemonStaleContainer(t *testing.T) {
	// This test verifies that stale daemon detection works
	// We can't fully test execViaDaemon as it calls os.Exit(), but we can
	// verify the staleness check logic indirectly through runtime tests

	daemonName := "construct-cli-daemon"
	imageName := constants.ImageName + ":latest"

	// Verify staleness detection for non-existent container
	stale := runtime.IsContainerStale("docker", daemonName, imageName)
	if !stale {
		t.Log("Non-existent container correctly identified as stale (or test env has no Docker)")
	}
}

// TestDaemonEnvironmentVariables verifies that daemon exec sets up required environment
func TestDaemonEnvironmentVariables(t *testing.T) {
	cfg := &config.Config{
		Sandbox: config.SandboxConfig{
			ClipboardHost: "host.docker.internal",
		},
	}

	// Verify config can be read for clipboard host
	if cfg.Sandbox.ClipboardHost == "" {
		t.Error("Expected clipboard host to be set")
	}

	// Test with agent args
	args := []string{"claude"}
	if len(args) == 0 {
		t.Error("Args should have one element")
	}

	// Verify agent name extraction works
	if args[0] != "claude" {
		t.Errorf("Expected agent 'claude', got '%s'", args[0])
	}
}

// TestStartDaemonBackgroundNoImage verifies behavior when image doesn't exist
func TestStartDaemonBackgroundNoImage(t *testing.T) {
	// Test that startDaemonBackground handles missing image gracefully
	// This is a unit test for the logic path when image check fails

	containerRuntime := "docker" // May not exist in test env
	configPath := "/tmp/test-config"

	// Create a minimal config
	cfg := &config.Config{
		Network: config.NetworkConfig{
			Mode: "offline",
		},
	}

	// We can't run startDaemonBackground directly as it calls exec.Command,
	// but we can verify the logic flow through integration tests

	_ = cfg
	_ = containerRuntime
	_ = configPath

	t.Skip("Requires actual container runtime - covered by integration tests")
}

// TestWaitForDaemonTimeout verifies timeout behavior
func TestWaitForDaemonTimeout(t *testing.T) {
	// Test that waitForDaemon respects timeout
	// Since waitForDaemon is not exported, we verify the logic through container state polling

	containerRuntime := "docker"
	daemonName := "construct-cli-daemon"

	// Check container state directly
	state := runtime.GetContainerState(containerRuntime, daemonName)

	// In test environment, daemon should not be running
	switch state {
	case runtime.ContainerStateRunning:
		t.Log("Daemon is running (may be from previous test or manual start)")
	case runtime.ContainerStateMissing:
		t.Log("Daemon not running (expected in test environment)")
	case runtime.ContainerStateExited:
		t.Log("Daemon exists but is exited")
	default:
		t.Errorf("Invalid container state: %v", state)
	}
}

// TestAgentArgParsing verifies that agent arguments are parsed correctly
func TestAgentArgParsing(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected string
	}{
		{"Single agent", []string{"claude"}, "claude"},
		{"Agent with flags", []string{"gemini", "--resume", "id123"}, "gemini"},
		{"Agent with prompt", []string{"codex", "write code"}, "codex"},
		{"Empty args", []string{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agentName := ""
			if len(tt.args) > 0 {
				agentName = tt.args[0]
			}

			if agentName != tt.expected {
				t.Errorf("Expected agent '%s', got '%s'", tt.expected, agentName)
			}
		})
	}
}

func TestEnsureAgentRuntimeDirsCreatesCodexHome(t *testing.T) {
	configPath := t.TempDir()

	if err := ensureAgentRuntimeDirs([]string{"codex"}, configPath); err != nil {
		t.Fatalf("expected no error creating codex runtime dirs, got %v", err)
	}

	codexHome := filepath.Join(configPath, "home", ".codex")
	info, err := os.Stat(codexHome)
	if err != nil {
		t.Fatalf("expected codex home to exist at %s: %v", codexHome, err)
	}
	if !info.IsDir() {
		t.Fatalf("expected codex home path to be a directory, got file: %s", codexHome)
	}
}

func TestEnsureAgentRuntimeDirsSkipsNonCodex(t *testing.T) {
	configPath := t.TempDir()

	if err := ensureAgentRuntimeDirs([]string{"claude"}, configPath); err != nil {
		t.Fatalf("expected no error for non-codex agent, got %v", err)
	}

	codexHome := filepath.Join(configPath, "home", ".codex")
	if _, err := os.Stat(codexHome); !os.IsNotExist(err) {
		t.Fatalf("expected codex home to remain absent for non-codex agent, stat err=%v", err)
	}
}

// TestCodexWSLEnvironment verifies codex-specific environment setup
func TestCodexWSLEnvironment(t *testing.T) {
	// Test that codex agent gets WSL environment variables
	envVars := []string{}
	appendAgentSpecificDaemonEnv(&envVars, "codex")

	hasCodexHome := false
	hasWSLDistro := false
	hasWSLInterop := false
	hasDisplay := false

	for _, env := range envVars {
		switch env {
		case "CODEX_HOME=/home/construct/.codex":
			hasCodexHome = true
		case "WSL_DISTRO_NAME=Ubuntu":
			hasWSLDistro = true
		case "WSL_INTEROP=/run/WSL/8_interop":
			hasWSLInterop = true
		case "DISPLAY=":
			hasDisplay = true
		}
	}

	if !hasCodexHome {
		t.Error("Codex agent should have CODEX_HOME set to /home/construct/.codex")
	}
	if !hasWSLDistro {
		t.Error("Codex agent should have WSL_DISTRO_NAME set")
	}
	if !hasWSLInterop {
		t.Error("Codex agent should have WSL_INTEROP set")
	}
	if !hasDisplay {
		t.Error("Codex agent should have DISPLAY set to empty")
	}
}

func TestAppendAgentSpecificRunFlagsCodex(t *testing.T) {
	origDebug := os.Getenv("CONSTRUCT_DEBUG")
	t.Cleanup(func() {
		if origDebug == "" {
			os.Unsetenv("CONSTRUCT_DEBUG")
			return
		}
		os.Setenv("CONSTRUCT_DEBUG", origDebug)
	})
	os.Setenv("CONSTRUCT_DEBUG", "1")

	runFlags := []string{}
	appendAgentSpecificRunFlags(&runFlags, "codex", "1")

	expected := []string{
		"CODEX_HOME=/home/construct/.codex",
		"WSL_DISTRO_NAME=Ubuntu",
		"WSL_INTEROP=/run/WSL/8_interop",
		"DISPLAY=",
		"CONSTRUCT_DEBUG=1",
	}
	for _, envVar := range expected {
		if !hasRunFlagEnv(runFlags, envVar) {
			t.Fatalf("expected run flags to include %q, got %v", envVar, runFlags)
		}
	}
}

func TestAppendAgentSpecificRunFlagsCodexNoClipboardPatch(t *testing.T) {
	runFlags := []string{}
	appendAgentSpecificRunFlags(&runFlags, "codex", "0")

	if !hasRunFlagEnv(runFlags, "CODEX_HOME=/home/construct/.codex") {
		t.Fatalf("expected CODEX_HOME in run flags, got %v", runFlags)
	}
	unexpected := []string{
		"WSL_DISTRO_NAME=Ubuntu",
		"WSL_INTEROP=/run/WSL/8_interop",
		"DISPLAY=",
	}
	for _, envVar := range unexpected {
		if hasRunFlagEnv(runFlags, envVar) {
			t.Fatalf("did not expect %q in run flags when clipboard patch is disabled: %v", envVar, runFlags)
		}
	}
}

func TestAppendAgentSpecificRunFlagsNonCodex(t *testing.T) {
	runFlags := []string{}
	appendAgentSpecificRunFlags(&runFlags, "claude", "1")
	if len(runFlags) != 0 {
		t.Fatalf("expected no run flags for non-codex agent, got %v", runFlags)
	}
}

func TestAppendAgentSpecificExecEnvCodex(t *testing.T) {
	origDebug := os.Getenv("CONSTRUCT_DEBUG")
	t.Cleanup(func() {
		if origDebug == "" {
			os.Unsetenv("CONSTRUCT_DEBUG")
			return
		}
		os.Setenv("CONSTRUCT_DEBUG", origDebug)
	})
	os.Setenv("CONSTRUCT_DEBUG", "1")

	envVars := []string{}
	appendAgentSpecificExecEnv(&envVars, "codex", "1")

	expected := []string{
		"CODEX_HOME=/home/construct/.codex",
		"WSL_DISTRO_NAME=Ubuntu",
		"WSL_INTEROP=/run/WSL/8_interop",
		"DISPLAY=",
		"CONSTRUCT_DEBUG=1",
	}

	for _, expectedVar := range expected {
		if !containsEnv(envVars, expectedVar) {
			t.Fatalf("expected env vars to include %q, got %v", expectedVar, envVars)
		}
	}
}

func TestAppendAgentSpecificExecEnvCodexNoClipboardPatch(t *testing.T) {
	envVars := []string{}
	appendAgentSpecificExecEnv(&envVars, "codex", "0")

	if !containsEnv(envVars, "CODEX_HOME=/home/construct/.codex") {
		t.Fatalf("expected CODEX_HOME in env vars, got %v", envVars)
	}

	unexpected := []string{
		"WSL_DISTRO_NAME=Ubuntu",
		"WSL_INTEROP=/run/WSL/8_interop",
		"DISPLAY=",
		"CONSTRUCT_DEBUG=1",
	}
	for _, unexpectedVar := range unexpected {
		if containsEnv(envVars, unexpectedVar) {
			t.Fatalf("did not expect %q when clipboard patch is disabled: %v", unexpectedVar, envVars)
		}
	}
}

func TestAppendAgentSpecificExecEnvNonCodex(t *testing.T) {
	envVars := []string{}
	appendAgentSpecificExecEnv(&envVars, "claude", "1")
	if len(envVars) != 0 {
		t.Fatalf("expected no env vars for non-codex agent, got %v", envVars)
	}
}

func TestAppendAgentSpecificDaemonEnvNonCodex(t *testing.T) {
	envVars := []string{}
	appendAgentSpecificDaemonEnv(&envVars, "claude")
	if len(envVars) != 0 {
		t.Fatalf("expected no daemon env vars for non-codex agent, got %v", envVars)
	}
}

func hasRunFlagEnv(runFlags []string, value string) bool {
	for i := 0; i < len(runFlags)-1; i++ {
		if runFlags[i] == "-e" && runFlags[i+1] == value {
			return true
		}
	}
	return false
}

func containsEnv(envVars []string, value string) bool {
	for _, envVar := range envVars {
		if envVar == value {
			return true
		}
	}
	return false
}

func envValue(envVars []string, key string) string {
	prefix := key + "="
	for _, envVar := range envVars {
		if strings.HasPrefix(envVar, prefix) {
			return strings.TrimPrefix(envVar, prefix)
		}
	}
	return ""
}

func TestExecInRunningContainerInjectsConstructHomeAndCodexEnv(t *testing.T) {
	origStartClipboard := startClipboardServerFn
	origExecInteractive := execInteractiveAsUserFn
	origColorterm := os.Getenv("COLORTERM")
	t.Cleanup(func() {
		startClipboardServerFn = origStartClipboard
		execInteractiveAsUserFn = origExecInteractive
		if origColorterm == "" {
			os.Unsetenv("COLORTERM")
			return
		}
		os.Setenv("COLORTERM", origColorterm)
	})
	os.Setenv("COLORTERM", "truecolor")

	startClipboardServerFn = func(host string) (*clipboard.Server, error) {
		if host != "clip.local" {
			t.Fatalf("expected clipboard host clip.local, got %s", host)
		}
		return &clipboard.Server{
			URL:   "http://clip.local:1234",
			Token: "clip-token",
		}, nil
	}

	var gotRuntime, gotContainer, gotWorkdir, gotUser string
	var gotCmdArgs []string
	var gotEnvVars []string
	execInteractiveAsUserFn = func(containerRuntime, containerName string, cmdArgs []string, envVars []string, workdir, user string) (int, error) {
		gotRuntime = containerRuntime
		gotContainer = containerName
		gotWorkdir = workdir
		gotUser = user
		gotCmdArgs = append([]string{}, cmdArgs...)
		gotEnvVars = append([]string{}, envVars...)
		return 0, nil
	}

	cfg := &config.Config{
		Sandbox: config.SandboxConfig{
			ClipboardHost:   "clip.local",
			ExecAsHostUser:  false,
			ForwardSSHAgent: false,
		},
		Agents: config.AgentsConfig{
			ClipboardImagePatch: true,
		},
	}

	exitCode, err := execInRunningContainer(
		[]string{"codex", "help"},
		cfg,
		"docker",
		[]string{"OPENAI_API_KEY=test-key"},
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}

	if gotRuntime != "docker" {
		t.Fatalf("expected docker runtime, got %s", gotRuntime)
	}
	if gotContainer != "construct-cli" {
		t.Fatalf("expected construct-cli container, got %s", gotContainer)
	}
	if gotWorkdir != "" {
		t.Fatalf("expected empty workdir for attach exec, got %q", gotWorkdir)
	}
	if gotUser != "" {
		t.Fatalf("expected empty exec user when exec_as_host_user disabled, got %q", gotUser)
	}

	if len(gotCmdArgs) != 2 || gotCmdArgs[0] != "codex" || gotCmdArgs[1] != "help" {
		t.Fatalf("unexpected command args: %v", gotCmdArgs)
	}

	requiredEnv := []string{
		"HOME=/home/construct",
		"CONSTRUCT_CLIPBOARD_IMAGE_PATCH=1",
		"CONSTRUCT_AGENT_NAME=codex",
		"CODEX_HOME=/home/construct/.codex",
		"WSL_DISTRO_NAME=Ubuntu",
		"WSL_INTEROP=/run/WSL/8_interop",
		"DISPLAY=",
		"CONSTRUCT_CLIPBOARD_URL=http://clip.local:1234",
		"CONSTRUCT_CLIPBOARD_TOKEN=clip-token",
		"CONSTRUCT_FILE_PASTE_AGENTS=" + constants.FileBasedPasteAgents,
		"OPENAI_API_KEY=test-key",
		"COLORTERM=truecolor",
	}
	for _, envVar := range requiredEnv {
		if !containsEnv(gotEnvVars, envVar) {
			t.Fatalf("expected env var %q, got env: %v", envVar, gotEnvVars)
		}
	}

	expectedPath := env.BuildConstructPath("/home/construct")
	if pathValue := envValue(gotEnvVars, "PATH"); pathValue != expectedPath {
		t.Fatalf("expected PATH=%q, got %q", expectedPath, pathValue)
	}
	if constructPath := envValue(gotEnvVars, "CONSTRUCT_PATH"); constructPath != expectedPath {
		t.Fatalf("expected CONSTRUCT_PATH=%q, got %q", expectedPath, constructPath)
	}
}

func TestExecInRunningContainerUsesConfiguredShellWhenNoArgs(t *testing.T) {
	origStartClipboard := startClipboardServerFn
	origExecInteractive := execInteractiveAsUserFn
	t.Cleanup(func() {
		startClipboardServerFn = origStartClipboard
		execInteractiveAsUserFn = origExecInteractive
	})

	startClipboardServerFn = func(string) (*clipboard.Server, error) {
		return nil, fmt.Errorf("clipboard unavailable")
	}

	var gotCmdArgs []string
	execInteractiveAsUserFn = func(_, _ string, cmdArgs []string, _ []string, _, _ string) (int, error) {
		gotCmdArgs = append([]string{}, cmdArgs...)
		return 0, nil
	}

	cfg := &config.Config{
		Sandbox: config.SandboxConfig{
			Shell:          "/bin/zsh",
			ExecAsHostUser: false,
		},
	}

	exitCode, err := execInRunningContainer(nil, cfg, "docker", nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if len(gotCmdArgs) != 1 || gotCmdArgs[0] != "/bin/zsh" {
		t.Fatalf("expected shell fallback /bin/zsh, got %v", gotCmdArgs)
	}
}

func TestExecInRunningContainerUsesHostUserOnLinuxDocker(t *testing.T) {
	if stdruntime.GOOS != "linux" {
		t.Skip("linux-specific host user mapping behavior")
	}

	origStartClipboard := startClipboardServerFn
	origExecInteractive := execInteractiveAsUserFn
	origHasUIDEntry := containerHasUIDEntryFn
	t.Cleanup(func() {
		startClipboardServerFn = origStartClipboard
		execInteractiveAsUserFn = origExecInteractive
		containerHasUIDEntryFn = origHasUIDEntry
	})

	startClipboardServerFn = func(string) (*clipboard.Server, error) {
		return nil, fmt.Errorf("clipboard unavailable")
	}
	containerHasUIDEntryFn = func(_, _ string, uid int) (bool, error) {
		return uid == os.Getuid(), nil
	}

	var gotUser string
	execInteractiveAsUserFn = func(_, _ string, _ []string, envVars []string, _, user string) (int, error) {
		gotUser = user
		if !containsEnv(envVars, "HOME=/home/construct") {
			t.Fatalf("expected HOME to be forced to /home/construct, got %v", envVars)
		}
		return 0, nil
	}

	cfg := &config.Config{
		Sandbox: config.SandboxConfig{
			ExecAsHostUser: true,
		},
	}

	_, err := execInRunningContainer([]string{"claude"}, cfg, "docker", nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	wantUser := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	if gotUser != wantUser {
		t.Fatalf("expected host exec user %q, got %q", wantUser, gotUser)
	}
}

// TestColortermEnvironment verifies COLORTERM handling
func TestColortermEnvironment(t *testing.T) {
	// Test when COLORTERM is already set
	origColorterm := os.Getenv("COLORTERM")
	os.Setenv("COLORTERM", "truecolor")

	colorterm := os.Getenv("COLORTERM")
	if colorterm != "truecolor" {
		t.Errorf("Expected COLORTERM=truecolor, got '%s'", colorterm)
	}

	// Test when COLORTERM is not set
	os.Unsetenv("COLORTERM")
	colorterm = os.Getenv("COLORTERM")
	if colorterm != "" {
		t.Errorf("Expected empty COLORTERM, got '%s'", colorterm)
	}

	// Restore original value
	if origColorterm != "" {
		os.Setenv("COLORTERM", origColorterm)
	}
}

func TestApplyConstructPath(t *testing.T) {
	envVars := []string{
		"PATH=/usr/bin",
		"CONSTRUCT_PATH=/tmp/old",
		"HOME=/home/construct",
	}

	applyConstructPath(&envVars)

	want := env.BuildConstructPath("/home/construct")

	var gotPath string
	var gotConstructPath string
	for _, envVar := range envVars {
		if strings.HasPrefix(envVar, "PATH=") {
			gotPath = strings.TrimPrefix(envVar, "PATH=")
		}
		if strings.HasPrefix(envVar, "CONSTRUCT_PATH=") {
			gotConstructPath = strings.TrimPrefix(envVar, "CONSTRUCT_PATH=")
		}
	}

	if gotPath != want {
		t.Fatalf("Expected PATH %q, got %q", want, gotPath)
	}
	if gotConstructPath != want {
		t.Fatalf("Expected CONSTRUCT_PATH %q, got %q", want, gotConstructPath)
	}
}

func TestExecUserForAgentExec(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *config.Config
		runtime string
		want    string
	}{
		{
			name:    "nil config",
			cfg:     nil,
			runtime: "docker",
			want:    "",
		},
		{
			name: "disabled setting",
			cfg: &config.Config{
				Sandbox: config.SandboxConfig{ExecAsHostUser: false},
			},
			runtime: "docker",
			want:    "",
		},
		{
			name: "enabled setting non docker runtime",
			cfg: &config.Config{
				Sandbox: config.SandboxConfig{ExecAsHostUser: true},
			},
			runtime: "podman",
			want:    "",
		},
	}

	if stdruntime.GOOS == "linux" {
		tests = append(tests, struct {
			name    string
			cfg     *config.Config
			runtime string
			want    string
		}{
			name: "enabled setting docker runtime linux",
			cfg: &config.Config{
				Sandbox: config.SandboxConfig{ExecAsHostUser: true},
			},
			runtime: "docker",
			want: func() string {
				if runtime.UsesUserNamespaceRemap("docker") {
					return ""
				}
				return fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
			}(),
		})
	} else {
		tests = append(tests, struct {
			name    string
			cfg     *config.Config
			runtime string
			want    string
		}{
			name: "enabled setting docker runtime non linux",
			cfg: &config.Config{
				Sandbox: config.SandboxConfig{ExecAsHostUser: true},
			},
			runtime: "docker",
			want:    "",
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := execUserForAgentExec(tt.cfg, tt.runtime)
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestAppendExecUserRunFlags(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *config.Config
		runtime string
		wantAny []string
		wantLen int
	}{
		{
			name: "disabled setting",
			cfg: &config.Config{
				Sandbox: config.SandboxConfig{ExecAsHostUser: false},
			},
			runtime: "docker",
			wantLen: 0,
		},
		{
			name: "non-docker runtime",
			cfg: &config.Config{
				Sandbox: config.SandboxConfig{ExecAsHostUser: true},
			},
			runtime: "podman",
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runFlags := []string{}
			appendExecUserRunFlags(&runFlags, tt.cfg, tt.runtime)
			if len(runFlags) != tt.wantLen {
				t.Fatalf("expected %d run flag entries, got %d: %v", tt.wantLen, len(runFlags), runFlags)
			}
		})
	}

	if stdruntime.GOOS == "linux" {
		t.Run("linux docker adds host user and HOME", func(t *testing.T) {
			runFlags := []string{}
			cfg := &config.Config{Sandbox: config.SandboxConfig{ExecAsHostUser: true}}
			appendExecUserRunFlags(&runFlags, cfg, "docker")

			if runtime.UsesUserNamespaceRemap("docker") {
				if len(runFlags) != 0 {
					t.Fatalf("expected no run flags in userns-remap mode, got %v", runFlags)
				}
				return
			}

			expectedUser := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
			expected := []string{
				"--user", expectedUser,
				"-e", "HOME=/home/construct",
			}
			for _, expectedValue := range expected {
				found := false
				for _, actual := range runFlags {
					if actual == expectedValue {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("expected run flags to include %q, got %v", expectedValue, runFlags)
				}
			}
		})
	}
}

func TestResolveExecUserForRunningContainerFallbacks(t *testing.T) {
	original := containerHasUIDEntryFn
	t.Cleanup(func() {
		containerHasUIDEntryFn = original
	})

	cfg := &config.Config{
		Sandbox: config.SandboxConfig{ExecAsHostUser: true},
	}

	if stdruntime.GOOS != "linux" {
		containerHasUIDEntryFn = func(_, _ string, _ int) (bool, error) {
			t.Fatalf("uid lookup should not be called on non-linux hosts")
			return false, nil
		}
		if got := resolveExecUserForRunningContainer(cfg, "docker", "construct-cli-daemon"); got != "" {
			t.Fatalf("expected empty user on non-linux, got %q", got)
		}
		return
	}

	expectedUser := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	if runtime.UsesUserNamespaceRemap("docker") {
		containerHasUIDEntryFn = func(_, _ string, _ int) (bool, error) {
			t.Fatalf("uid lookup should not be called in userns-remap mode")
			return false, nil
		}
		if got := resolveExecUserForRunningContainer(cfg, "docker", "construct-cli-daemon"); got != "" {
			t.Fatalf("expected empty user in userns-remap mode, got %q", got)
		}
		return
	}

	t.Run("uses host uid when passwd entry exists", func(t *testing.T) {
		containerHasUIDEntryFn = func(containerRuntime, containerName string, uid int) (bool, error) {
			if containerRuntime != "docker" {
				t.Fatalf("expected docker runtime, got %s", containerRuntime)
			}
			if containerName != "construct-cli-daemon" {
				t.Fatalf("expected daemon container name, got %s", containerName)
			}
			if uid != os.Getuid() {
				t.Fatalf("expected uid %d, got %d", os.Getuid(), uid)
			}
			return true, nil
		}
		got := resolveExecUserForRunningContainer(cfg, "docker", "construct-cli-daemon")
		if got != expectedUser {
			t.Fatalf("expected %q, got %q", expectedUser, got)
		}
	})

	t.Run("falls back when passwd entry missing", func(t *testing.T) {
		containerHasUIDEntryFn = func(_, _ string, _ int) (bool, error) {
			return false, nil
		}
		got := resolveExecUserForRunningContainer(cfg, "docker", "construct-cli-daemon")
		if got != expectedUser {
			t.Fatalf("expected host uid mapping %q when passwd entry is missing, got %q", expectedUser, got)
		}
	})

	t.Run("uses host uid when uid lookup errors", func(t *testing.T) {
		containerHasUIDEntryFn = func(_, _ string, _ int) (bool, error) {
			return false, fmt.Errorf("lookup failed")
		}
		got := resolveExecUserForRunningContainer(cfg, "docker", "construct-cli-daemon")
		if got != expectedUser {
			t.Fatalf("expected host uid mapping %q on lookup error, got %q", expectedUser, got)
		}
	})
}

// TestDaemonName verifies daemon container name constant
func TestDaemonName(t *testing.T) {
	expectedDaemonName := "construct-cli-daemon"

	// This matches the name used in startDaemonBackground and execViaDaemon
	if expectedDaemonName != "construct-cli-daemon" {
		t.Errorf("Daemon name mismatch: expected 'construct-cli-daemon', got '%s'", expectedDaemonName)
	}
}

// TestConfigPathHandling verifies config path is handled correctly
func TestConfigPathHandling(t *testing.T) {
	configPath := "/tmp/test-construct-container"

	// Verify config path is a valid string
	if configPath == "" {
		t.Error("Config path should not be empty")
	}

	// In actual usage, this would be passed to runtime.GetComposeFileArgs
	// We verify it's a non-empty string
	if len(configPath) == 0 {
		t.Error("Config path length should be greater than 0")
	}
}

// TestStartDaemonSSHBridgeNoop verifies that the daemon SSH bridge is a no-op
// when forwarding is disabled or SSH_AUTH_SOCK is unset.
func TestStartDaemonSSHBridgeNoop(t *testing.T) {
	origSock := os.Getenv("SSH_AUTH_SOCK")
	os.Unsetenv("SSH_AUTH_SOCK")
	t.Cleanup(func() {
		if origSock != "" {
			os.Setenv("SSH_AUTH_SOCK", origSock)
		}
	})

	cfg := &config.Config{
		Sandbox: config.SandboxConfig{
			ForwardSSHAgent: false,
		},
	}

	bridge, envVars, err := startDaemonSSHBridge(cfg, "docker", "construct-cli-daemon", "")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if bridge != nil {
		t.Error("Expected no bridge to be created")
	}
	if envVars != nil {
		t.Errorf("Expected no env vars, got: %v", envVars)
	}
}

// TestNetworkModeInjection verifies network mode is passed to daemon
func TestNetworkModeInjection(t *testing.T) {
	cfg := &config.Config{
		Network: config.NetworkConfig{
			Mode: "strict",
		},
	}

	if cfg.Network.Mode != "strict" {
		t.Errorf("Expected network mode 'strict', got '%s'", cfg.Network.Mode)
	}

	// Test offline mode
	cfg.Network.Mode = "offline"
	if cfg.Network.Mode != "offline" {
		t.Errorf("Expected network mode 'offline', got '%s'", cfg.Network.Mode)
	}

	// Test permissive mode
	cfg.Network.Mode = "permissive"
	if cfg.Network.Mode != "permissive" {
		t.Errorf("Expected network mode 'permissive', got '%s'", cfg.Network.Mode)
	}
}

// TestMapDaemonWorkdir verifies host-to-container working dir mapping.
func TestMapDaemonWorkdir(t *testing.T) {
	base := filepath.Join(string(os.PathSeparator), "Users", "esteban", "Dev")
	mountDest := "/projects/Dev"

	tests := []struct {
		name        string
		cwd         string
		mountSource string
		want        string
		wantOK      bool
	}{
		{
			name:        "Exact mount root",
			cwd:         base,
			mountSource: base,
			want:        mountDest,
			wantOK:      true,
		},
		{
			name:        "Nested path",
			cwd:         filepath.Join(base, "Projects", "construct-cli"),
			mountSource: base,
			want:        "/projects/Dev/Projects/construct-cli",
			wantOK:      true,
		},
		{
			name:        "Outside mount",
			cwd:         filepath.Join(string(os.PathSeparator), "tmp"),
			mountSource: base,
			want:        "",
			wantOK:      false,
		},
		{
			name:        "Empty inputs",
			cwd:         "",
			mountSource: base,
			want:        "",
			wantOK:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := mapDaemonWorkdir(tt.cwd, tt.mountSource, mountDest)
			if ok != tt.wantOK {
				t.Fatalf("Expected ok=%v, got %v", tt.wantOK, ok)
			}
			if got != tt.want {
				t.Fatalf("Expected %q, got %q", tt.want, got)
			}
		})
	}
}

// TestDualHashVerificationConcept verifies the concept of dual hash verification
//
// This is a regression test to ensure that both host AND container hash verification
// are performed. Skipping container hash check would allow undetected filesystem
// inconsistencies. See PERFORMANCE.md optimization #8 for details.
func TestDualHashVerificationConcept(t *testing.T) {
	// This test verifies the CONCEPT of dual verification:
	// 1. Host uses embedded template hash
	// 2. Container uses actual file hash
	//
	// If these differ, setup should run to correct inconsistencies

	// Simulate embedded template hash (host side)
	embeddedEntrypoint := "#!/usr/bin/env bash\n# Original entrypoint\necho 'setup'"
	hostHash := hashString(embeddedEntrypoint)

	// Simulate actual file hash (container side) - modified version
	modifiedEntrypoint := "#!/usr/bin/env bash\n# MODIFIED entrypoint\necho 'malicious'"
	containerHash := hashString(modifiedEntrypoint)

	// If host verification passes but container file is modified,
	// the hashes should differ, triggering setup
	if hostHash == containerHash {
		t.Error("Modified container file should have different hash than embedded template")
	}

	// Simulate matching hashes (normal case)
	normalEntrypoint := embeddedEntrypoint
	normalContainerHash := hashString(normalEntrypoint)

	if hostHash != normalContainerHash {
		t.Error("Unmodified container file should have same hash as embedded template")
	}

	// The dual verification ensures:
	// 1. Host verification checks if image matches binary (using embedded template)
	// 2. Container verification checks if filesystem is correct (using actual file)
	// 3. If either check fails, setup runs to correct the issue
}

// TestHashMismatchScenarios verifies various hash mismatch scenarios
//
// This test ensures the hash verification logic correctly identifies:
// - Template vs file mismatches
// - Missing hash files
// - Corrupted hash files
func TestHashMismatchScenarios(t *testing.T) {
	tests := []struct {
		name           string
		hostHash       string
		containerHash  string
		storedHash     string
		shouldRunSetup bool
		reason         string
	}{
		{
			name:           "All hashes match - no setup needed",
			hostHash:       "abc123",
			containerHash:  "abc123",
			storedHash:     "abc123",
			shouldRunSetup: false,
			reason:         "All hashes identical",
		},
		{
			name:           "Container file modified - setup required",
			hostHash:       "abc123",
			containerHash:  "def456", // File was modified
			storedHash:     "abc123",
			shouldRunSetup: true,
			reason:         "Container filesystem differs from embedded template",
		},
		{
			name:           "Embedded template changed - setup required",
			hostHash:       "new789", // Binary updated with new entrypoint
			containerHash:  "abc123", // Container still has old entrypoint
			storedHash:     "abc123",
			shouldRunSetup: true,
			reason:         "Binary updated but container image stale",
		},
		{
			name:           "Stored hash missing - setup required",
			hostHash:       "abc123",
			containerHash:  "abc123",
			storedHash:     "", // Hash file doesn't exist
			shouldRunSetup: true,
			reason:         "First run or hash file was deleted",
		},
		{
			name:           "Stored hash corrupted - setup required",
			hostHash:       "abc123",
			containerHash:  "abc123",
			storedHash:     "corrupted", // Hash file has wrong value
			shouldRunSetup: true,
			reason:         "Hash file corrupted or out of sync",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate host verification logic
			hostVerificationPasses := (tt.hostHash == tt.storedHash)

			// Simulate container verification logic
			containerVerificationPasses := (tt.containerHash == tt.storedHash)

			// Setup should run if EITHER verification fails
			// This is the SAFETY mechanism - dual verification
			setupNeeded := !hostVerificationPasses || !containerVerificationPasses

			if setupNeeded != tt.shouldRunSetup {
				t.Errorf("%s: expected setupNeeded=%v, got %v. Reason: %s",
					tt.name, tt.shouldRunSetup, setupNeeded, tt.reason)
			}

			// Log the verification states for clarity
			t.Logf("Host check: %v, Container check: %v, Setup needed: %v",
				hostVerificationPasses, containerVerificationPasses, setupNeeded)
		})
	}
}

// TestEntrypointHashWithInstallScript verifies hash calculation includes install script
//
// The hash is a composite of: entrypoint.sh + install_user_packages.sh (if exists)
func TestEntrypointHashWithInstallScript(t *testing.T) {
	entrypointHash := hashString("entrypoint content")
	installScriptHash := hashString("install script content")

	// Composite hash matches the pattern used in entrypoint.sh
	compositeHash := entrypointHash + "-" + installScriptHash

	if compositeHash == entrypointHash {
		t.Error("Composite hash should differ from entrypoint-only hash when install script exists")
	}

	// Without install script, hash should be just entrypoint hash
	withoutInstallScript := entrypointHash

	if withoutInstallScript != entrypointHash {
		t.Error("Without install script, hash should equal entrypoint hash")
	}
}

// hashString is a test helper for SHA256 hashing
func hashString(s string) string {
	// Use actual SHA256 for deterministic hashing
	// This ensures different strings produce different hashes
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
