package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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

func containsEnv(env []string, item string) bool {
	for _, entry := range env {
		if entry == item {
			return true
		}
	}
	return false
}
