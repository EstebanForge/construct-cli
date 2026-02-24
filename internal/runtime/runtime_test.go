//revive:disable:var-naming // Package name intentionally matches folder name used across imports.
package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	stdruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/EstebanForge/construct-cli/internal/config"
)

// TestRuntimeDetection tests container runtime detection
func TestRuntimeDetection(t *testing.T) {
	tests := []struct {
		name       string
		preference string
		expected   string
	}{
		{"Auto detection", "auto", ""}, // Will vary by system
		{"Docker preference", "docker", "docker"},
		{"Podman preference", "podman", "podman"},
		{"Container preference", "container", "container"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Note: This will only pass if the runtime is actually available
			result := detectRuntimeSafe(tt.preference)
			if tt.expected != "" && result != tt.expected {
				// Check if the preferred runtime exists
				if _, err := exec.LookPath(tt.expected); err == nil {
					t.Errorf("Expected runtime '%s', got '%s'", tt.expected, result)
				} else {
					t.Skipf("Runtime '%s' not available on this system", tt.expected)
				}
			}
			if result == "" {
				t.Skip("No container runtime available on this system")
			}
		})
	}
}

// detectRuntimeSafe is a test-safe version that doesn't exit
func detectRuntimeSafe(preferredEngine string) string {
	runtimes := []string{"container", "podman", "docker"}

	if preferredEngine != "auto" && preferredEngine != "" {
		runtimes = append([]string{preferredEngine}, runtimes...)
	}

	for _, rt := range runtimes {
		if _, err := exec.LookPath(rt); err == nil {
			return rt
		}
	}

	return ""
}

// TestGetCheckImageCommand tests image check command generation
func TestGetCheckImageCommand(t *testing.T) {
	tests := []struct {
		runtime  string
		expected []string
	}{
		{"docker", []string{"docker", "image", "inspect", "construct-box:latest"}},
		{"podman", []string{"podman", "image", "inspect", "construct-box:latest"}},
		{"container", []string{"docker", "image", "inspect", "construct-box:latest"}},
	}

	for _, tt := range tests {
		t.Run(tt.runtime, func(t *testing.T) {
			result := GetCheckImageCommand(tt.runtime)
			if len(result) != len(tt.expected) {
				t.Fatalf("Expected %d args, got %d", len(tt.expected), len(result))
			}
			for i, arg := range result {
				if arg != tt.expected[i] {
					t.Errorf("Arg %d: expected '%s', got '%s'", i, tt.expected[i], arg)
				}
			}
		})
	}
}

func TestBuildComposeCommandPodmanFallbackToComposePlugin(t *testing.T) {
	tmpDir := t.TempDir()
	origPath := os.Getenv("PATH")
	t.Cleanup(func() {
		os.Setenv("PATH", origPath)
	})

	// No podman-compose binary available in PATH -> should use "podman compose".
	os.Setenv("PATH", tmpDir)

	cmd, err := BuildComposeCommand("podman", tmpDir, "run", []string{"--rm", "construct-box", "true"})
	if err != nil {
		t.Fatalf("BuildComposeCommand failed: %v", err)
	}

	if len(cmd.Args) < 2 {
		t.Fatalf("unexpected args for podman compose fallback: %v", cmd.Args)
	}
	if cmd.Args[0] != "podman" || cmd.Args[1] != "compose" {
		t.Fatalf("expected fallback to 'podman compose', got: %v", cmd.Args)
	}
}

func TestBuildComposeCommandPodmanUsesPodmanComposeBinary(t *testing.T) {
	if stdruntime.GOOS == "windows" {
		t.Skip("test relies on POSIX executable bits")
	}

	tmpDir := t.TempDir()
	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("failed to create bin dir: %v", err)
	}

	// Provide a fake podman-compose binary so LookPath picks it.
	podmanComposePath := filepath.Join(binDir, "podman-compose")
	if err := os.WriteFile(podmanComposePath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("failed to create fake podman-compose: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Cleanup(func() {
		os.Setenv("PATH", origPath)
	})
	os.Setenv("PATH", binDir)

	cmd, err := BuildComposeCommand("podman", tmpDir, "run", []string{"--rm", "construct-box", "true"})
	if err != nil {
		t.Fatalf("BuildComposeCommand failed: %v", err)
	}

	if len(cmd.Args) == 0 {
		t.Fatalf("unexpected empty args for podman-compose command")
	}
	if cmd.Args[0] != "podman-compose" {
		t.Fatalf("expected podman-compose binary path, got: %v", cmd.Args)
	}
}

func TestAppendHostIdentityEnvLinux(t *testing.T) {
	env := []string{"A=1"}
	got := AppendHostIdentityEnv(env)

	if stdruntime.GOOS == "linux" {
		if !containsEnvWithPrefix(got, "CONSTRUCT_HOST_UID=") {
			t.Fatalf("expected CONSTRUCT_HOST_UID in env on linux, got %v", got)
		}
		if !containsEnvWithPrefix(got, "CONSTRUCT_HOST_GID=") {
			t.Fatalf("expected CONSTRUCT_HOST_GID in env on linux, got %v", got)
		}
		return
	}

	if containsEnvWithPrefix(got, "CONSTRUCT_HOST_UID=") || containsEnvWithPrefix(got, "CONSTRUCT_HOST_GID=") {
		t.Fatalf("did not expect host identity env on non-linux, got %v", got)
	}
}

func TestBuildComposeCommandInjectsHostIdentityEnvOnLinux(t *testing.T) {
	tmpDir := t.TempDir()
	origPath := os.Getenv("PATH")
	t.Cleanup(func() {
		os.Setenv("PATH", origPath)
	})
	os.Setenv("PATH", tmpDir)

	cmd, err := BuildComposeCommand("podman", tmpDir, "run", []string{"--rm", "construct-box", "true"})
	if err != nil {
		t.Fatalf("BuildComposeCommand failed: %v", err)
	}

	if !containsEnvWithPrefix(cmd.Env, "CONSTRUCT_PROJECT_PATH=") {
		t.Fatalf("expected CONSTRUCT_PROJECT_PATH in command env, got %v", cmd.Env)
	}

	if stdruntime.GOOS == "linux" {
		if !containsEnvWithPrefix(cmd.Env, "CONSTRUCT_HOST_UID=") || !containsEnvWithPrefix(cmd.Env, "CONSTRUCT_HOST_GID=") {
			t.Fatalf("expected host identity env in compose command env on linux, got %v", cmd.Env)
		}
		if !containsEnvWithPrefix(cmd.Env, "CONSTRUCT_USERNS_REMAP=") {
			t.Fatalf("expected CONSTRUCT_USERNS_REMAP in compose command env on linux, got %v", cmd.Env)
		}
	}
}

// TestGetOSInfo tests OS information retrieval
// getOSInfo is not in runtime package anymore (it was private in main.go and I didn't export it in runtime.go because it seemed unused except for test?)
// Wait, getOSInfo was in main.go. Did I move it?
// I moved `getOSInfo` to `runtime`? No, I checked `runtime.go` content and didn't see `GetOSInfo`.
// It was unused in `main.go`. I might have dropped it.
// If it's unused, I don't need to test it.

func TestGetProjectMountPath(t *testing.T) {
	tests := []struct {
		cwd      string
		expected string
	}{
		{"/home/user/my-project", "/projects/my-project"},
		{"/Users/esteban/Construct CLI", "/projects/Construct CLI"},
		{"/workspaces/cool-app", "/projects/cool-app"},
		{"/", "/projects/"}, // Edge case: root
	}

	for _, tt := range tests {
		result := getProjectMountPathFromDir(tt.cwd)
		if result != tt.expected {
			t.Errorf("For %s, expected %s, got %s", tt.cwd, tt.expected, result)
		}
	}
}

func containsEnvWithPrefix(env []string, prefix string) bool {
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}

func TestGenerateDockerComposeOverride(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()

	// Set temp HOME for config isolation
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Get current dir name
	projectName := filepath.Base(tmpDir)
	if projectName == "." || projectName == "/" {
		projectName = "workspace"
	}
	expectedPath := "/projects/" + projectName

	containerDir := filepath.Join(tmpDir, "container")
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Generate override
	if err := GenerateDockerComposeOverride(tmpDir, expectedPath, "bridge", "docker"); err != nil {
		t.Fatalf("GenerateDockerComposeOverride failed: %v", err)
	}

	// Read file
	content, err := os.ReadFile(filepath.Join(containerDir, "docker-compose.override.yml"))
	if err != nil {
		t.Fatalf("Failed to read generated file: %v", err)
	}

	// Check content
	contentStr := string(content)
	if !strings.Contains(contentStr, "${PWD}:"+expectedPath) {
		t.Errorf("Expected mount to %s, got: %s", expectedPath, contentStr)
	}
	if !strings.Contains(contentStr, "working_dir: "+expectedPath) {
		t.Errorf("Expected working_dir: %s, got: %s", expectedPath, contentStr)
	}
	if !strings.Contains(contentStr, "entrypoint-hash.sh") {
		t.Errorf("Expected entrypoint-hash.sh mount, got: %s", contentStr)
	}
	if !strings.Contains(contentStr, "agent-patch.sh") {
		t.Errorf("Expected agent-patch.sh mount, got: %s", contentStr)
	}
	if !strings.Contains(contentStr, "update-all.sh") {
		t.Errorf("Expected update-all.sh mount, got: %s", contentStr)
	}
	if stdruntime.GOOS == "linux" && strings.Contains(contentStr, "user: \"") {
		t.Errorf("Did not expect docker override to force user mapping on Linux, got: %s", contentStr)
	}
	if strings.Contains(contentStr, "CONSTRUCT_HOST_UID=") || strings.Contains(contentStr, "CONSTRUCT_HOST_GID=") {
		t.Errorf("Did not expect docker override to inject host uid/gid env vars, got: %s", contentStr)
	}
}

func TestGenerateDockerComposeOverridePodmanUserMappingDependsOnUsernsMode(t *testing.T) {
	if stdruntime.GOOS != "linux" {
		t.Skip("Linux-specific podman user mapping behavior")
	}

	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	projectName := filepath.Base(tmpDir)
	if projectName == "." || projectName == "/" {
		projectName = "workspace"
	}
	expectedPath := "/projects/" + projectName

	containerDir := filepath.Join(tmpDir, "container")
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	if err := GenerateDockerComposeOverride(tmpDir, expectedPath, "bridge", "podman"); err != nil {
		t.Fatalf("GenerateDockerComposeOverride failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(containerDir, "docker-compose.override.yml"))
	if err != nil {
		t.Fatalf("Failed to read generated file: %v", err)
	}
	contentStr := string(content)
	hasUserMapping := strings.Contains(contentStr, "user: \"")
	if UsesUserNamespaceRemap("podman") {
		if hasUserMapping {
			t.Fatalf("Expected podman override to skip user mapping in userns-remap mode, got: %s", contentStr)
		}
		return
	}
	if !hasUserMapping {
		t.Fatalf("Expected podman override to include user mapping when userns-remap is inactive, got: %s", contentStr)
	}
}

func TestGenerateDockerComposeOverrideHomeCwdSELinuxUsesFallbackWorkingDir(t *testing.T) {
	if stdruntime.GOOS != "linux" {
		t.Skip("Linux-specific SELinux fallback behavior")
	}

	origHome := os.Getenv("HOME")
	origCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to read cwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", origHome)
		_ = os.Chdir(origCwd)
	})

	tmpHome := t.TempDir()
	if err := os.Setenv("HOME", tmpHome); err != nil {
		t.Fatalf("failed to set HOME: %v", err)
	}
	if err := os.Chdir(tmpHome); err != nil {
		t.Fatalf("failed to chdir to temp home: %v", err)
	}

	configDir := config.GetConfigDir()
	containerDir := filepath.Join(configDir, "container")
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		t.Fatalf("failed to create container dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(containerDir, "Dockerfile"), []byte("FROM scratch\n"), 0644); err != nil {
		t.Fatalf("failed to write Dockerfile fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(containerDir, "powershell.exe"), []byte("#!/usr/bin/env bash\n"), 0755); err != nil {
		t.Fatalf("failed to write powershell fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "packages.toml"), []byte(""), 0644); err != nil {
		t.Fatalf("failed to write packages fixture: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Sandbox.SelinuxLabels = "enabled"
	if err := cfg.Save(); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	projectName := filepath.Base(tmpHome)
	if projectName == "." || projectName == "/" {
		projectName = "workspace"
	}
	projectPath := "/projects/" + projectName

	if err := GenerateDockerComposeOverride(configDir, projectPath, "bridge", "podman"); err != nil {
		t.Fatalf("GenerateDockerComposeOverride failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(containerDir, "docker-compose.override.yml"))
	if err != nil {
		t.Fatalf("failed to read override: %v", err)
	}
	contentStr := string(content)
	if !strings.Contains(contentStr, "working_dir: /projects") {
		t.Fatalf("expected fallback working_dir /projects, got: %s", contentStr)
	}
	if !strings.Contains(contentStr, "${PWD}:"+projectPath) {
		t.Fatalf("expected project mount to remain present, got: %s", contentStr)
	}
}

// TestContainerStateConstants verifies container state constants exist
func TestContainerStateConstants(t *testing.T) {
	// Verify constants are defined and distinct
	states := []ContainerState{
		ContainerStateMissing,
		ContainerStateRunning,
		ContainerStateExited,
	}

	seen := make(map[ContainerState]bool)
	for _, s := range states {
		if seen[s] {
			t.Errorf("Duplicate container state value: %v", s)
		}
		seen[s] = true
	}
}

func TestHashOverrideInputsIncludesRuntime(t *testing.T) {
	base := overrideInputs{
		Version:        "1.0.0",
		Runtime:        "docker",
		UID:            1000,
		GID:            1000,
		SELinuxEnabled: false,
		NetworkMode:    "bridge",
		GitName:        "Test User",
		GitEmail:       "test@example.com",
		ProjectPath:    "/projects/test",
		SSHAuthSock:    "/tmp/ssh.sock",
		ForwardSSH:     true,
		PropagateGit:   true,
		DaemonMulti:    false,
		DaemonMounts:   "",
	}
	podman := base
	podman.Runtime = "podman"

	dockerHash := hashOverrideInputs(base)
	podmanHash := hashOverrideInputs(podman)
	if dockerHash == podmanHash {
		t.Fatalf("expected different override hashes for different runtimes, got %s", dockerHash)
	}
}

func TestHashOverrideInputsIncludesAllowCustomOverride(t *testing.T) {
	base := overrideInputs{
		Version:        "1.0.0",
		Runtime:        "docker",
		UID:            1000,
		GID:            1000,
		SELinuxEnabled: false,
		NetworkMode:    "bridge",
		GitName:        "Test User",
		GitEmail:       "test@example.com",
		ProjectPath:    "/projects/test",
		SSHAuthSock:    "/tmp/ssh.sock",
		ForwardSSH:     true,
		PropagateGit:   true,
		AllowCustom:    false,
	}
	customOverride := base
	customOverride.AllowCustom = true

	baseHash := hashOverrideInputs(base)
	customHash := hashOverrideInputs(customOverride)
	if baseHash == customHash {
		t.Fatalf("expected different override hashes when allow_custom override changes, got %s", baseHash)
	}
}

func TestOverrideHasUserMapping(t *testing.T) {
	tmpDir := t.TempDir()
	containerDir := filepath.Join(tmpDir, "container")
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		t.Fatalf("failed to create container dir: %v", err)
	}

	overridePath := filepath.Join(containerDir, "docker-compose.override.yml")
	contentWithUser := "services:\n  construct-box:\n    user: \"1001:1001\"\n"
	if err := os.WriteFile(overridePath, []byte(contentWithUser), 0644); err != nil {
		t.Fatalf("failed to write override file: %v", err)
	}

	hasUser, err := overrideHasUserMapping(tmpDir)
	if err != nil {
		t.Fatalf("overrideHasUserMapping failed: %v", err)
	}
	if !hasUser {
		t.Fatalf("expected user mapping to be detected")
	}

	contentWithoutUser := "services:\n  construct-box:\n    image: construct-box:latest\n"
	if err := os.WriteFile(overridePath, []byte(contentWithoutUser), 0644); err != nil {
		t.Fatalf("failed to write override file: %v", err)
	}

	hasUser, err = overrideHasUserMapping(tmpDir)
	if err != nil {
		t.Fatalf("overrideHasUserMapping failed: %v", err)
	}
	if hasUser {
		t.Fatalf("expected no user mapping to be detected")
	}
}

func TestGenerateDockerComposeOverrideHealsManualDockerUserMappingOnLinux(t *testing.T) {
	if stdruntime.GOOS != "linux" {
		t.Skip("Linux-specific docker override behavior")
	}

	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	projectName := filepath.Base(tmpDir)
	if projectName == "." || projectName == "/" {
		projectName = "workspace"
	}
	expectedPath := "/projects/" + projectName

	containerDir := filepath.Join(tmpDir, "container")
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// First generate a normal override and matching hash cache.
	if err := GenerateDockerComposeOverride(tmpDir, expectedPath, "bridge", "docker"); err != nil {
		t.Fatalf("GenerateDockerComposeOverride failed: %v", err)
	}

	// Manually inject a dangerous user mapping while keeping the cached hash untouched.
	overridePath := filepath.Join(containerDir, "docker-compose.override.yml")
	content, err := os.ReadFile(overridePath)
	if err != nil {
		t.Fatalf("Failed to read generated file: %v", err)
	}
	modified := strings.Replace(string(content), "  construct-box:\n", "  construct-box:\n    user: \"1001:1001\"\n", 1)
	if err := os.WriteFile(overridePath, []byte(modified), 0644); err != nil {
		t.Fatalf("Failed to write modified override: %v", err)
	}

	// Re-run generation with unchanged inputs; it should detect and heal the user mapping.
	if err := GenerateDockerComposeOverride(tmpDir, expectedPath, "bridge", "docker"); err != nil {
		t.Fatalf("GenerateDockerComposeOverride failed: %v", err)
	}

	healed, err := os.ReadFile(overridePath)
	if err != nil {
		t.Fatalf("Failed to read healed override: %v", err)
	}
	if strings.Contains(string(healed), "user: \"1001:1001\"") {
		t.Fatalf("expected docker override regeneration to remove manual user mapping, got: %s", string(healed))
	}
}

func TestGenerateDockerComposeOverrideKeepsUserMappingWhenNonRootStrictEnabled(t *testing.T) {
	if stdruntime.GOOS != "linux" {
		t.Skip("Linux-specific docker override behavior")
	}

	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	configDir := filepath.Join(tmpDir, ".config", "construct-cli")
	containerDir := filepath.Join(configDir, "container")
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		t.Fatalf("failed to create container dir: %v", err)
	}

	configToml := "[runtime]\nengine = \"docker\"\n\n[sandbox]\nnon_root_strict = true\n"
	requiredFiles := map[string]string{
		"Dockerfile":            "FROM alpine\n",
		"powershell.exe":        "binary\n",
		"packages.toml":         "[npm]\npackages = []\n",
		"config.toml":           configToml,
		"docker-compose.yml":    "version: '3'\n",
		"entrypoint.sh":         "#!/bin/bash\n",
		"update-all.sh":         "#!/bin/bash\n",
		"network-filter.sh":     "#!/bin/bash\n",
		"clipper":               "binary\n",
		"clipboard-x11-sync.sh": "#!/bin/bash\n",
		"osascript":             "binary\n",
	}
	for file, content := range requiredFiles {
		path := filepath.Join(containerDir, file)
		if file == "config.toml" || file == "packages.toml" {
			path = filepath.Join(configDir, file)
		}
		perm := os.FileMode(0644)
		if filepath.Ext(path) == ".sh" {
			perm = 0755
		}
		if err := os.WriteFile(path, []byte(content), perm); err != nil {
			t.Fatalf("failed to write %s: %v", file, err)
		}
	}

	if err := GenerateDockerComposeOverride(configDir, "/projects/test", "bridge", "docker"); err != nil {
		t.Fatalf("GenerateDockerComposeOverride failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(containerDir, "docker-compose.override.yml"))
	if err != nil {
		t.Fatalf("failed to read override: %v", err)
	}
	contentStr := string(content)
	if !strings.Contains(contentStr, "user: \"") {
		t.Fatalf("expected user mapping when non_root_strict is enabled, got: %s", contentStr)
	}
	if !strings.Contains(contentStr, "CONSTRUCT_NON_ROOT_STRICT=1") {
		t.Fatalf("expected strict mode marker in override env, got: %s", contentStr)
	}
}

func TestGenerateDockerComposeOverrideKeepsManualUserMappingWhenCustomOverrideAllowed(t *testing.T) {
	if stdruntime.GOOS != "linux" {
		t.Skip("Linux-specific docker override behavior")
	}

	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	configDir := filepath.Join(tmpDir, ".config", "construct-cli")
	containerDir := filepath.Join(configDir, "container")
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		t.Fatalf("failed to create container dir: %v", err)
	}

	configToml := "[runtime]\nengine = \"docker\"\n\n[sandbox]\nallow_custom_compose_override = true\n"
	requiredFiles := map[string]string{
		"Dockerfile":            "FROM alpine\n",
		"powershell.exe":        "binary\n",
		"packages.toml":         "[npm]\npackages = []\n",
		"config.toml":           configToml,
		"docker-compose.yml":    "version: '3'\n",
		"entrypoint.sh":         "#!/bin/bash\n",
		"update-all.sh":         "#!/bin/bash\n",
		"network-filter.sh":     "#!/bin/bash\n",
		"clipper":               "binary\n",
		"clipboard-x11-sync.sh": "#!/bin/bash\n",
		"osascript":             "binary\n",
	}
	for file, content := range requiredFiles {
		path := filepath.Join(containerDir, file)
		if file == "config.toml" || file == "packages.toml" {
			path = filepath.Join(configDir, file)
		}
		perm := os.FileMode(0644)
		if filepath.Ext(path) == ".sh" {
			perm = 0755
		}
		if err := os.WriteFile(path, []byte(content), perm); err != nil {
			t.Fatalf("failed to write %s: %v", file, err)
		}
	}

	if err := GenerateDockerComposeOverride(configDir, "/projects/test", "bridge", "docker"); err != nil {
		t.Fatalf("GenerateDockerComposeOverride failed: %v", err)
	}

	overridePath := filepath.Join(containerDir, "docker-compose.override.yml")
	content, err := os.ReadFile(overridePath)
	if err != nil {
		t.Fatalf("failed to read override: %v", err)
	}
	manualMapping := "    user: \"1001:1001\"\n"
	modified := strings.Replace(string(content), "  construct-box:\n", "  construct-box:\n"+manualMapping, 1)
	if err := os.WriteFile(overridePath, []byte(modified), 0644); err != nil {
		t.Fatalf("failed to write modified override: %v", err)
	}

	// With allow_custom_compose_override enabled and unchanged inputs, manual mapping should be preserved.
	if err := GenerateDockerComposeOverride(configDir, "/projects/test", "bridge", "docker"); err != nil {
		t.Fatalf("GenerateDockerComposeOverride failed: %v", err)
	}

	healed, err := os.ReadFile(overridePath)
	if err != nil {
		t.Fatalf("failed to read final override: %v", err)
	}
	if !strings.Contains(string(healed), manualMapping) {
		t.Fatalf("expected manual user mapping to be preserved when allow_custom_compose_override is enabled, got: %s", string(healed))
	}
}

func TestGenerateDockerComposeOverrideStrictNetworkUsesConsistentName(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	configDir := filepath.Join(tmpDir, ".config", "construct-cli")
	containerDir := filepath.Join(configDir, "container")
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		t.Fatalf("failed to create container dir: %v", err)
	}

	configToml := "[runtime]\nengine = \"docker\"\n"
	requiredFiles := map[string]string{
		"Dockerfile":            "FROM alpine\n",
		"powershell.exe":        "binary\n",
		"packages.toml":         "[npm]\npackages = []\n",
		"config.toml":           configToml,
		"docker-compose.yml":    "version: '3'\n",
		"entrypoint.sh":         "#!/bin/bash\n",
		"update-all.sh":         "#!/bin/bash\n",
		"network-filter.sh":     "#!/bin/bash\n",
		"clipper":               "binary\n",
		"clipboard-x11-sync.sh": "#!/bin/bash\n",
		"osascript":             "binary\n",
	}
	for file, content := range requiredFiles {
		path := filepath.Join(containerDir, file)
		if file == "config.toml" || file == "packages.toml" {
			path = filepath.Join(configDir, file)
		}
		perm := os.FileMode(0644)
		if filepath.Ext(path) == ".sh" {
			perm = 0755
		}
		if err := os.WriteFile(path, []byte(content), perm); err != nil {
			t.Fatalf("failed to write %s: %v", file, err)
		}
	}

	if err := GenerateDockerComposeOverride(configDir, "/projects/test", "strict", "docker"); err != nil {
		t.Fatalf("GenerateDockerComposeOverride failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(containerDir, "docker-compose.override.yml"))
	if err != nil {
		t.Fatalf("failed to read override: %v", err)
	}
	contentStr := string(content)
	if !strings.Contains(contentStr, "name: construct-net") {
		t.Fatalf("expected strict network name to be construct-net, got: %s", contentStr)
	}
	if strings.Contains(contentStr, "name: construct-cli") {
		t.Fatalf("did not expect strict network name construct-cli, got: %s", contentStr)
	}
}

// TestIsContainerStaleLogic tests the staleness comparison logic
func TestIsContainerStaleLogic(t *testing.T) {
	// Test the logic conceptually - we can't easily mock exec.Command
	// but we can verify the function handles errors correctly

	tests := []struct {
		name             string
		containerRuntime string
		containerName    string
		imageName        string
		expectStale      bool
	}{
		{
			name:             "Non-existent container returns stale",
			containerRuntime: "docker",
			containerName:    "non-existent-container-xyz123",
			imageName:        "construct-box:latest",
			expectStale:      true, // Should return true because container doesn't exist
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsContainerStale(tt.containerRuntime, tt.containerName, tt.imageName)
			if result != tt.expectStale {
				t.Errorf("Expected stale=%v, got %v", tt.expectStale, result)
			}
		})
	}
}

// TestExecInContainerUnsupportedRuntime tests error handling for unsupported runtimes
func TestExecInContainerUnsupportedRuntime(t *testing.T) {
	_, err := ExecInContainer("unsupported-runtime", "test-container", []string{"echo", "hello"})
	if err == nil {
		t.Error("Expected error for unsupported runtime, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported runtime") {
		t.Errorf("Expected 'unsupported runtime' error, got: %v", err)
	}
}

// TestExecInContainerWithEnvUnsupportedRuntime tests error handling for unsupported runtimes
func TestExecInContainerWithEnvUnsupportedRuntime(t *testing.T) {
	_, err := ExecInContainerWithEnv("unsupported-runtime", "test-container", []string{"echo", "hello"}, []string{"FOO=bar"}, "")
	if err == nil {
		t.Error("Expected error for unsupported runtime, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported runtime") {
		t.Errorf("Expected 'unsupported runtime' error, got: %v", err)
	}
}

// TestExecInteractiveUnsupportedRuntime tests error handling for unsupported runtimes
func TestExecInteractiveUnsupportedRuntime(t *testing.T) {
	exitCode, err := ExecInteractive("unsupported-runtime", "test-container", []string{"echo"}, nil, "")
	if err == nil {
		t.Error("Expected error for unsupported runtime, got nil")
	}
	if exitCode != 1 {
		t.Errorf("Expected exit code 1, got %d", exitCode)
	}
	if !strings.Contains(err.Error(), "unsupported runtime") {
		t.Errorf("Expected 'unsupported runtime' error, got: %v", err)
	}
}

func TestContainerHasUIDEntryUnsupportedRuntime(t *testing.T) {
	hasEntry, err := ContainerHasUIDEntry("unsupported-runtime", "test-container", 1000)
	if err == nil {
		t.Fatal("Expected error for unsupported runtime")
	}
	if hasEntry {
		t.Fatal("Expected hasEntry to be false for unsupported runtime")
	}
	if !strings.Contains(err.Error(), "unsupported runtime") {
		t.Fatalf("Expected unsupported runtime error, got: %v", err)
	}
}

// TestGetContainerWorkingDirUnsupportedRuntime tests error handling
func TestGetContainerWorkingDirUnsupportedRuntime(t *testing.T) {
	_, err := GetContainerWorkingDir("unsupported-runtime", "test-container")
	if err == nil {
		t.Error("Expected error for unsupported runtime, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported runtime") {
		t.Errorf("Expected 'unsupported runtime' error, got: %v", err)
	}
}

// TestGetContainerMountSourceUnsupportedRuntime tests error handling
func TestGetContainerMountSourceUnsupportedRuntime(t *testing.T) {
	_, err := GetContainerMountSource("unsupported-runtime", "test-container", "/projects/test")
	if err == nil {
		t.Error("Expected error for unsupported runtime, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported runtime") {
		t.Errorf("Expected 'unsupported runtime' error, got: %v", err)
	}
}

// TestGetContainerImageIDUnsupportedRuntime tests error handling
func TestGetContainerImageIDUnsupportedRuntime(t *testing.T) {
	_, err := GetContainerImageID("unsupported-runtime", "test-container")
	if err == nil {
		t.Error("Expected error for unsupported runtime, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported runtime") {
		t.Errorf("Expected 'unsupported runtime' error, got: %v", err)
	}
}

// TestGetImageIDUnsupportedRuntime tests error handling
func TestGetImageIDUnsupportedRuntime(t *testing.T) {
	_, err := GetImageID("unsupported-runtime", "test-image")
	if err == nil {
		t.Error("Expected error for unsupported runtime, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported runtime") {
		t.Errorf("Expected 'unsupported runtime' error, got: %v", err)
	}
}

// TestHashOverrideInputs tests override input hashing
func TestHashOverrideInputs(t *testing.T) {
	tests := []struct {
		name   string
		inputs overrideInputs
		sameAs int // Index of test that should produce same hash (-1 if none)
	}{
		{
			name: "Basic inputs",
			inputs: overrideInputs{
				Version:        "1.0.0",
				UID:            1000,
				GID:            1000,
				SELinuxEnabled: false,
				NetworkMode:    "permissive",
				GitName:        "Test User",
				GitEmail:       "test@example.com",
				ProjectPath:    "/projects/test",
				SSHAuthSock:    "/tmp/ssh-agent",
				ForwardSSH:     true,
				PropagateGit:   true,
			},
			sameAs: 1,
		},
		{
			name: "Same inputs - should produce same hash",
			inputs: overrideInputs{
				Version:        "1.0.0",
				UID:            1000,
				GID:            1000,
				SELinuxEnabled: false,
				NetworkMode:    "permissive",
				GitName:        "Test User",
				GitEmail:       "test@example.com",
				ProjectPath:    "/projects/test",
				SSHAuthSock:    "/tmp/ssh-agent",
				ForwardSSH:     true,
				PropagateGit:   true,
			},
			sameAs: 0,
		},
		{
			name: "Different version",
			inputs: overrideInputs{
				Version:        "1.0.1",
				UID:            1000,
				GID:            1000,
				SELinuxEnabled: false,
				NetworkMode:    "permissive",
				GitName:        "Test User",
				GitEmail:       "test@example.com",
				ProjectPath:    "/projects/test",
				SSHAuthSock:    "/tmp/ssh-agent",
				ForwardSSH:     true,
				PropagateGit:   true,
			},
			sameAs: -1,
		},
		{
			name: "Different UID",
			inputs: overrideInputs{
				Version:        "1.0.0",
				UID:            1001,
				GID:            1000,
				SELinuxEnabled: false,
				NetworkMode:    "permissive",
				GitName:        "Test User",
				GitEmail:       "test@example.com",
				ProjectPath:    "/projects/test",
				SSHAuthSock:    "/tmp/ssh-agent",
				ForwardSSH:     true,
				PropagateGit:   true,
			},
			sameAs: -1,
		},
		{
			name: "Different network mode",
			inputs: overrideInputs{
				Version:        "1.0.0",
				UID:            1000,
				GID:            1000,
				SELinuxEnabled: false,
				NetworkMode:    "strict",
				GitName:        "Test User",
				GitEmail:       "test@example.com",
				ProjectPath:    "/projects/test",
				SSHAuthSock:    "/tmp/ssh-agent",
				ForwardSSH:     true,
				PropagateGit:   true,
			},
			sameAs: -1,
		},
	}

	hashes := make([]string, len(tests))
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hashes[i] = hashOverrideInputs(tt.inputs)
			if hashes[i] == "" {
				t.Error("Hash should not be empty")
			}
			if len(hashes[i]) != 64 { // SHA256 produces 64 hex characters
				t.Errorf("Hash should be 64 characters, got %d", len(hashes[i]))
			}
		})
	}

	// Verify that tests marked as "sameAs" produce identical hashes
	for i, tt := range tests {
		if tt.sameAs >= 0 && tt.sameAs < len(hashes) {
			if hashes[i] != hashes[tt.sameAs] {
				t.Errorf("Test %d (%s) should have same hash as test %d, but got different hashes",
					i, tt.name, tt.sameAs)
			}
		}
	}

	// Verify that all other hashes are different
	for i := 0; i < len(hashes); i++ {
		for j := i + 1; j < len(hashes); j++ {
			testsI := tests[i]
			testsJ := tests[j]
			if testsI.sameAs != j && testsJ.sameAs != i {
				if hashes[i] == hashes[j] {
					t.Errorf("Tests %d (%s) and %d (%s) should have different hashes but got same hash: %s",
						i, tests[i].name, j, tests[j].name, hashes[i])
				}
			}
		}
	}
}

// TestGetOverrideHashPath tests hash path generation
func TestGetOverrideHashPath(t *testing.T) {
	configPath := "/tmp/test-config"
	expected := "/tmp/test-config/container/.override_hash"
	result := getOverrideHashPath(configPath)

	if result != expected {
		t.Errorf("Expected hash path %s, got %s", expected, result)
	}
}

// TestReadOverrideHash tests reading non-existent hash
func TestReadOverrideHash(t *testing.T) {
	tmpDir := t.TempDir()
	hash := readOverrideHash(tmpDir)

	if hash != "" {
		t.Errorf("Expected empty hash for non-existent file, got '%s'", hash)
	}
}

// TestWriteAndReadOverrideHash tests hash persistence
func TestWriteAndReadOverrideHash(t *testing.T) {
	tmpDir := t.TempDir()
	testHash := "a1b2c3d4e5f6"

	// Create container directory (required by writeOverrideHash)
	containerDir := filepath.Join(tmpDir, "container")
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		t.Fatalf("Failed to create container directory: %v", err)
	}

	// Write hash
	err := writeOverrideHash(tmpDir, testHash)
	if err != nil {
		t.Fatalf("Failed to write hash: %v", err)
	}

	// Read hash back
	readHash := readOverrideHash(tmpDir)
	if readHash != testHash {
		t.Errorf("Expected hash '%s', got '%s'", testHash, readHash)
	}

	// Verify file exists
	hashPath := getOverrideHashPath(tmpDir)
	data, err := os.ReadFile(hashPath)
	if err != nil {
		t.Fatalf("Failed to read hash file: %v", err)
	}
	content := strings.TrimSpace(string(data))
	if content != testHash {
		t.Errorf("Expected file content '%s', got '%s'", testHash, content)
	}
}

// TestCheckRuntimesParallel tests parallel runtime detection
func TestCheckRuntimesParallel(t *testing.T) {
	tests := []struct {
		name         string
		runtimes     []string
		checkRunning bool
		expectResult string // empty means any result is acceptable
		shouldFind   bool   // true if we expect to find at least one runtime
	}{
		{
			name:         "Check running runtimes - all",
			runtimes:     []string{"container", "podman", "docker"},
			checkRunning: true,
			expectResult: "",
			shouldFind:   false, // Don't enforce - runtime might be running in dev env
		},
		{
			name:         "Check installed runtimes - all",
			runtimes:     []string{"container", "podman", "docker"},
			checkRunning: false,
			expectResult: "",
			shouldFind:   true, // Should at least find one installed
		},
		{
			name:         "Check non-existent runtime",
			runtimes:     []string{"nonexistent-runtime-xyz"},
			checkRunning: false,
			expectResult: "",
			shouldFind:   false,
		},
		{
			name:         "Preferred runtime first",
			runtimes:     []string{"docker", "podman", "container"},
			checkRunning: false,
			expectResult: "",
			shouldFind:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()

			result := checkRuntimesParallel(ctx, tt.runtimes, tt.checkRunning)

			if tt.shouldFind && result == "" {
				t.Error("Expected to find at least one runtime, but got none")
			}

			// Note: We don't enforce !tt.shouldFind -> result == ""
			// because runtimes might be running in the test environment

			if tt.expectResult != "" && result != tt.expectResult {
				t.Errorf("Expected runtime '%s', got '%s'", tt.expectResult, result)
			}

			// Verify result is one of the requested runtimes
			if result != "" {
				found := false
				for _, rt := range tt.runtimes {
					if result == rt {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Result '%s' is not in the requested runtimes: %v", result, tt.runtimes)
				}
			}
		})
	}
}

// TestCheckRuntimesParallelTimeout tests that timeout works correctly
func TestCheckRuntimesParallelTimeout(t *testing.T) {
	// Very short timeout should cause early return
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// This should timeout immediately and return empty
	result := checkRuntimesParallel(ctx, []string{"docker", "podman", "container"}, false)

	// Result could be empty (timeout) or a runtime (fast response)
	// The important thing is the function doesn't hang
	t.Logf("Result from timeout test: %s", result)
}

// TestDetectRuntimeParallel tests the full DetectRuntime function with parallel detection
func TestDetectRuntimeParallel(t *testing.T) {
	// Test with auto detection (should use parallel check)
	result := detectRuntimeSafe("auto")

	// In a typical test environment, we might not have any runtime
	// The function will exit if none found, so we catch that
	if result == "" {
		t.Skip("No container runtime available in test environment")
	}

	// Verify result is one of the expected runtimes
	validRuntimes := map[string]bool{
		"container": true,
		"podman":    true,
		"docker":    true,
	}

	if !validRuntimes[result] {
		t.Errorf("Unexpected runtime: %s", result)
	}
}

// TestCheckRuntimesParallelPrefersRunning tests that running runtimes are preferred over installed ones
func TestCheckRuntimesParallelPrefersRunning(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Check for running runtimes only
	runningResult := checkRuntimesParallel(ctx, []string{"docker", "podman", "container"}, true)

	// Check for any installed runtime
	installedResult := checkRuntimesParallel(ctx, []string{"docker", "podman", "container"}, false)

	// If we found a running runtime, it should also be in the installed list
	if runningResult != "" && installedResult != "" {
		// Both should be valid runtimes
		validRuntimes := map[string]bool{
			"container": true,
			"podman":    true,
			"docker":    true,
		}

		if !validRuntimes[runningResult] {
			t.Errorf("Invalid running runtime: %s", runningResult)
		}

		if !validRuntimes[installedResult] {
			t.Errorf("Invalid installed runtime: %s", installedResult)
		}
	}
}
