package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	stdruntime "runtime"
	"strings"
	"testing"

	runtimepkg "github.com/EstebanForge/construct-cli/internal/runtime"
)

func TestComposeUserMappingParsesValue(t *testing.T) {
	tmpDir := t.TempDir()
	overridePath := filepath.Join(tmpDir, "docker-compose.override.yml")
	content := "services:\n  construct-box:\n    user: \"1001:1001\"\n"
	if err := os.WriteFile(overridePath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write override file: %v", err)
	}

	mapping, err := composeUserMapping(overridePath)
	if err != nil {
		t.Fatalf("composeUserMapping returned error: %v", err)
	}
	if mapping != "1001:1001" {
		t.Fatalf("expected mapping 1001:1001, got %q", mapping)
	}
}

func TestComposeUserMappingHandlesMissingOrUnset(t *testing.T) {
	tmpDir := t.TempDir()

	missingPath := filepath.Join(tmpDir, "missing.yml")
	mapping, err := composeUserMapping(missingPath)
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	if mapping != "" {
		t.Fatalf("expected empty mapping for missing file, got %q", mapping)
	}

	overridePath := filepath.Join(tmpDir, "docker-compose.override.yml")
	content := "services:\n  construct-box:\n    image: construct-box:latest\n"
	if err := os.WriteFile(overridePath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write override file: %v", err)
	}

	mapping, err = composeUserMapping(overridePath)
	if err != nil {
		t.Fatalf("expected no error when user mapping is absent, got %v", err)
	}
	if mapping != "" {
		t.Fatalf("expected empty mapping when user key is absent, got %q", mapping)
	}
}

func TestParseComposeNetworkRecreationLines(t *testing.T) {
	output := `
Some preface
Network "container_default" needs to be recreated - option "com.docker.network.enable_ipv6" has changed
Network "container_default" needs to be recreated - option "com.docker.network.enable_ipv6" has changed
Network "container_default" needs to be recreated - option "com.docker.network.enable_ipv4" has changed
Other line
`

	lines := parseComposeNetworkRecreationLines(output)
	want := []string{
		`Network "container_default" needs to be recreated - option "com.docker.network.enable_ipv6" has changed`,
		`Network "container_default" needs to be recreated - option "com.docker.network.enable_ipv4" has changed`,
	}

	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("unexpected parsed lines: got %v want %v", lines, want)
	}
}

func TestExtractComposeNetworkNames(t *testing.T) {
	output := `
Network "container_default" needs to be recreated - option "com.docker.network.enable_ipv6" has changed
Network "container_default" needs to be recreated - option "com.docker.network.enable_ipv4" has changed
Network "another_network" needs to be recreated - option "com.docker.network.enable_ipv4" has changed
`

	names := extractComposeNetworkNames(output)
	want := []string{"container_default", "another_network"}

	if !reflect.DeepEqual(names, want) {
		t.Fatalf("unexpected network names: got %v want %v", names, want)
	}
}

func TestFixComposeNetworkRecreationIssueSuccess(t *testing.T) {
	origExec := execCombinedOutput
	origCompose := runDockerComposeCommand
	t.Cleanup(func() {
		execCombinedOutput = origExec
		runDockerComposeCommand = origCompose
	})

	var calls []string
	removedNetwork := false
	runDockerComposeCommand = func(args ...string) ([]byte, error) {
		call := "docker " + strings.Join(args, " ")
		calls = append(calls, call)

		if strings.Contains(call, " compose ") && strings.Contains(call, " up ") {
			return []byte(`Network "container_default" needs to be recreated - option "com.docker.network.enable_ipv6" has changed`), nil
		}
		if strings.Contains(call, " compose ") && strings.Contains(call, " down --remove-orphans") {
			return []byte("removed"), nil
		}
		return nil, fmt.Errorf("unexpected command: %s", call)
	}
	execCombinedOutput = func(name string, args ...string) ([]byte, error) {
		call := name + " " + strings.Join(args, " ")
		if strings.Contains(call, " network rm container_default") {
			removedNetwork = true
			return []byte("container_default"), nil
		}
		return nil, fmt.Errorf("unexpected command: %s", call)
	}

	fixed, details, unsupportedDryRun, err := fixComposeNetworkRecreationIssue()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if unsupportedDryRun {
		t.Fatalf("expected supported dry-run")
	}
	if !fixed {
		t.Fatalf("expected fixed=true")
	}
	if len(details) == 0 {
		t.Fatalf("expected details to be populated")
	}

	if len(calls) != 2 {
		t.Fatalf("expected 2 compose command calls, got %d (%v)", len(calls), calls)
	}
	if !removedNetwork {
		t.Fatalf("expected stale network removal command to be executed")
	}
}

func TestFixComposeNetworkRecreationIssueNoop(t *testing.T) {
	origExec := execCombinedOutput
	origCompose := runDockerComposeCommand
	t.Cleanup(func() {
		execCombinedOutput = origExec
		runDockerComposeCommand = origCompose
	})

	runDockerComposeCommand = func(_ ...string) ([]byte, error) {
		return []byte("no network issues"), nil
	}

	fixed, details, unsupportedDryRun, err := fixComposeNetworkRecreationIssue()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if unsupportedDryRun {
		t.Fatalf("expected supported dry-run")
	}
	if fixed {
		t.Fatalf("expected fixed=false")
	}
	if len(details) != 0 {
		t.Fatalf("expected no details, got %v", details)
	}
}

func TestFixComposeNetworkRecreationIssueDownFails(t *testing.T) {
	origExec := execCombinedOutput
	origCompose := runDockerComposeCommand
	t.Cleanup(func() {
		execCombinedOutput = origExec
		runDockerComposeCommand = origCompose
	})

	runDockerComposeCommand = func(args ...string) ([]byte, error) {
		call := "docker " + strings.Join(args, " ")
		if strings.Contains(call, " compose ") && strings.Contains(call, " up ") {
			return []byte(`Network "container_default" needs to be recreated - option "com.docker.network.enable_ipv4" has changed`), nil
		}
		if strings.Contains(call, " compose ") && strings.Contains(call, " down --remove-orphans") {
			return []byte("down failed"), fmt.Errorf("exit status 1")
		}
		return nil, fmt.Errorf("unexpected command: %s", call)
	}

	fixed, _, unsupportedDryRun, err := fixComposeNetworkRecreationIssue()
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if unsupportedDryRun {
		t.Fatalf("expected supported dry-run")
	}
	if fixed {
		t.Fatalf("expected fixed=false on failure")
	}
}

func TestSetEnvVar(t *testing.T) {
	env := []string{"A=1", "PWD=/tmp/old"}
	got := setEnvVar(env, "PWD", "/tmp/new")

	if !containsEnv(got, "PWD=/tmp/new") {
		t.Fatalf("expected updated PWD, got %v", got)
	}
}

func TestSetEnvVarAppendsWhenMissing(t *testing.T) {
	env := []string{"A=1"}
	got := setEnvVar(env, "CONSTRUCT_PROJECT_PATH", "/projects/repo")

	if !containsEnv(got, "CONSTRUCT_PROJECT_PATH=/projects/repo") {
		t.Fatalf("expected appended env var, got %v", got)
	}
}

func TestInspectLinuxConfigPermissionStateDetectsWrongOwner(t *testing.T) {
	configDir := t.TempDir()
	homeDir := filepath.Join(configDir, "home")
	containerDir := filepath.Join(configDir, "container")
	if err := os.MkdirAll(homeDir, 0755); err != nil {
		t.Fatalf("failed to create home dir: %v", err)
	}
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		t.Fatalf("failed to create container dir: %v", err)
	}

	origGetOwnerUID := getOwnerUID
	origCurrentUID := currentUID
	t.Cleanup(func() {
		getOwnerUID = origGetOwnerUID
		currentUID = origCurrentUID
	})

	currentUID = func() int { return 1000 }
	getOwnerUID = func(path string) (int, error) {
		if path == homeDir {
			return 0, nil
		}
		return 1000, nil
	}

	state := inspectLinuxConfigPermissionState(configDir)
	if len(state.WrongOwner) != 1 || state.WrongOwner[0] != homeDir {
		t.Fatalf("expected wrong owner on %s, got %v", homeDir, state.WrongOwner)
	}
	if !state.HasProblems() {
		t.Fatalf("expected HasProblems=true")
	}
}

func TestFixLinuxConfigOwnershipNoopWhenHealthy(t *testing.T) {
	if stdruntime.GOOS != "linux" {
		t.Skip("linux-specific fix behavior")
	}

	configDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configDir, "home"), 0755); err != nil {
		t.Fatalf("failed to create home dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(configDir, "container"), 0755); err != nil {
		t.Fatalf("failed to create container dir: %v", err)
	}

	origGetOwnerUID := getOwnerUID
	origCurrentUID := currentUID
	t.Cleanup(func() {
		getOwnerUID = origGetOwnerUID
		currentUID = origCurrentUID
	})
	currentUID = func() int { return 1000 }
	getOwnerUID = func(_ string) (int, error) { return 1000, nil }

	fixed, details, err := fixLinuxConfigOwnership(configDir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if fixed {
		t.Fatalf("expected fixed=false")
	}
	if len(details) != 0 {
		t.Fatalf("expected no details for noop, got %v", details)
	}
}

func TestFixLinuxConfigOwnershipUsesSudoAndChmod(t *testing.T) {
	if stdruntime.GOOS != "linux" {
		t.Skip("linux-specific fix behavior")
	}

	configDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configDir, "home"), 0755); err != nil {
		t.Fatalf("failed to create home dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(configDir, "container"), 0755); err != nil {
		t.Fatalf("failed to create container dir: %v", err)
	}

	origGetOwnerUID := getOwnerUID
	origCurrentUID := currentUID
	origCurrentGID := currentGID
	origRunSudoCombinedOutput := runSudoCombinedOutput
	origRunSudoInteractive := runSudoInteractive
	origRunChmodRecursive := runChmodRecursive
	origTTY := stdinIsTTY
	t.Cleanup(func() {
		getOwnerUID = origGetOwnerUID
		currentUID = origCurrentUID
		currentGID = origCurrentGID
		runSudoCombinedOutput = origRunSudoCombinedOutput
		runSudoInteractive = origRunSudoInteractive
		runChmodRecursive = origRunChmodRecursive
		stdinIsTTY = origTTY
	})

	currentUID = func() int { return 1000 }
	currentGID = func() int { return 1000 }
	stdinIsTTY = func() bool { return false }

	ownerFixed := false
	getOwnerUID = func(_ string) (int, error) {
		if ownerFixed {
			return 1000, nil
		}
		return 0, nil
	}

	var sudoCalls []string
	runSudoCombinedOutput = func(args ...string) ([]byte, error) {
		sudoCalls = append(sudoCalls, strings.Join(args, " "))
		if len(args) >= 5 && args[1] == "chown" {
			ownerFixed = true
			return []byte("ok"), nil
		}
		return []byte("ok"), nil
	}
	runSudoInteractive = func(_ ...string) error {
		t.Fatal("did not expect interactive sudo path")
		return nil
	}

	chmodCalled := false
	runChmodRecursive = func(path string) error {
		if path != configDir {
			t.Fatalf("expected chmod path %s, got %s", configDir, path)
		}
		chmodCalled = true
		return nil
	}

	fixed, details, err := fixLinuxConfigOwnership(configDir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !fixed {
		t.Fatalf("expected fixed=true")
	}
	if !chmodCalled {
		t.Fatalf("expected chmod step to run")
	}
	if len(sudoCalls) == 0 {
		t.Fatalf("expected sudo chown call")
	}
	if len(details) == 0 {
		t.Fatalf("expected fix details")
	}
}

func TestCleanupAgentContainerNoopWhenMissing(t *testing.T) {
	origGetState := getContainerStateFn
	origStop := stopContainerFn
	origCleanup := cleanupExitedContainerFn
	t.Cleanup(func() {
		getContainerStateFn = origGetState
		stopContainerFn = origStop
		cleanupExitedContainerFn = origCleanup
	})

	getContainerStateFn = func(_, _ string) runtimepkg.ContainerState {
		return runtimepkg.ContainerStateMissing
	}
	stopContainerFn = func(_, _ string) error {
		t.Fatal("did not expect stop call for missing container")
		return nil
	}
	cleanupExitedContainerFn = func(_, _ string) error {
		t.Fatal("did not expect cleanup call for missing container")
		return nil
	}

	cleaned, details, err := cleanupAgentContainer("docker")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cleaned {
		t.Fatalf("expected cleaned=false for missing container")
	}
	if len(details) != 0 {
		t.Fatalf("expected no details, got %v", details)
	}
}

func TestRecreateDaemonContainerRunning(t *testing.T) {
	origGetState := getContainerStateFn
	origStop := stopContainerFn
	origCleanup := cleanupExitedContainerFn
	origBuild := buildComposeCommandFn
	t.Cleanup(func() {
		getContainerStateFn = origGetState
		stopContainerFn = origStop
		cleanupExitedContainerFn = origCleanup
		buildComposeCommandFn = origBuild
	})

	stopCalled := false
	cleanupCalled := false
	buildCalled := false
	getContainerStateFn = func(_, name string) runtimepkg.ContainerState {
		if name != "construct-cli-daemon" {
			t.Fatalf("expected daemon container name, got %s", name)
		}
		return runtimepkg.ContainerStateRunning
	}
	stopContainerFn = func(_, name string) error {
		if name != "construct-cli-daemon" {
			t.Fatalf("expected daemon stop for construct-cli-daemon, got %s", name)
		}
		stopCalled = true
		return nil
	}
	cleanupExitedContainerFn = func(_, name string) error {
		if name != "construct-cli-daemon" {
			t.Fatalf("expected daemon cleanup for construct-cli-daemon, got %s", name)
		}
		cleanupCalled = true
		return nil
	}
	buildComposeCommandFn = func(runtimeName, _ string, subCommand string, _ []string) (*exec.Cmd, error) {
		if runtimeName != "docker" {
			t.Fatalf("expected docker runtime, got %s", runtimeName)
		}
		if subCommand != "run" {
			t.Fatalf("expected run subcommand, got %s", subCommand)
		}
		buildCalled = true
		return exec.Command("sh", "-c", "true"), nil
	}

	recreated, details, err := recreateDaemonContainer("docker", t.TempDir())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !recreated {
		t.Fatalf("expected recreated=true")
	}
	if !stopCalled || !cleanupCalled || !buildCalled {
		t.Fatalf("expected stop/cleanup/build calls, got stop=%t cleanup=%t build=%t", stopCalled, cleanupCalled, buildCalled)
	}
	if len(details) == 0 {
		t.Fatalf("expected recreate details")
	}
}

func TestRecreateDaemonContainerUsesProvidedConfigPath(t *testing.T) {
	origGetState := getContainerStateFn
	origStop := stopContainerFn
	origCleanup := cleanupExitedContainerFn
	origBuild := buildComposeCommandFn
	t.Cleanup(func() {
		getContainerStateFn = origGetState
		stopContainerFn = origStop
		cleanupExitedContainerFn = origCleanup
		buildComposeCommandFn = origBuild
	})

	t.Setenv("HOME", t.TempDir())
	configPath := t.TempDir()
	markerName := ".doctor-daemon-cwd"
	markerPath := filepath.Join(configPath, markerName)

	getContainerStateFn = func(_, _ string) runtimepkg.ContainerState {
		return runtimepkg.ContainerStateRunning
	}
	stopContainerFn = func(_, _ string) error { return nil }
	cleanupExitedContainerFn = func(_, _ string) error { return nil }
	buildComposeCommandFn = func(runtimeName, _ string, subCommand string, _ []string) (*exec.Cmd, error) {
		if runtimeName != "docker" {
			t.Fatalf("expected docker runtime, got %s", runtimeName)
		}
		if subCommand != "run" {
			t.Fatalf("expected run subcommand, got %s", subCommand)
		}
		return exec.Command("sh", "-c", "pwd > "+markerName), nil
	}

	recreated, _, err := recreateDaemonContainer("docker", configPath)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !recreated {
		t.Fatalf("expected recreated=true")
	}

	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("expected marker file at %s, got %v", markerPath, err)
	}
	gotDir := strings.TrimSpace(string(data))

	configInfo, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("failed to stat configPath %s: %v", configPath, err)
	}
	gotInfo, err := os.Stat(gotDir)
	if err != nil {
		t.Fatalf("failed to stat command cwd %s: %v", gotDir, err)
	}
	if !os.SameFile(configInfo, gotInfo) {
		t.Fatalf("expected command cwd to resolve to %s, got %s", configPath, gotDir)
	}
}

func TestRebuildImageForEntrypointFixWhenMissing(t *testing.T) {
	origGetCheck := getCheckImageCommandFn
	origBuild := buildComposeCommandFn
	t.Cleanup(func() {
		getCheckImageCommandFn = origGetCheck
		buildComposeCommandFn = origBuild
	})

	getCheckImageCommandFn = func(_ string) []string {
		return []string{"sh", "-c", "exit 1"}
	}

	buildCalled := false
	buildComposeCommandFn = func(runtimeName, _ string, subCommand string, _ []string) (*exec.Cmd, error) {
		if runtimeName != "docker" {
			t.Fatalf("expected docker runtime, got %s", runtimeName)
		}
		if subCommand != "build" {
			t.Fatalf("expected build subcommand, got %s", subCommand)
		}
		buildCalled = true
		return exec.Command("sh", "-c", "true"), nil
	}

	rebuilt, details, err := rebuildImageForEntrypointFix("docker", t.TempDir())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !rebuilt {
		t.Fatalf("expected rebuilt=true for missing image")
	}
	if !buildCalled {
		t.Fatalf("expected rebuild command to be executed")
	}
	if len(details) == 0 {
		t.Fatalf("expected rebuild details")
	}
}

func containsEnv(env []string, item string) bool {
	for _, entry := range env {
		if entry == item {
			return true
		}
	}
	return false
}
