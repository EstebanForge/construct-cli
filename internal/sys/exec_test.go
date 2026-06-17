package sys

import (
	"strings"
	"testing"
)

func TestExecCommandEmptyArgs(t *testing.T) {
	// Empty args should print usage and return 1.
	// No container required.
	code := ExecCommand(nil, nil)
	if code != 1 {
		t.Fatalf("expected exit code 1 for empty args, got %d", code)
	}
}

func TestExecCommandEmptySlice(t *testing.T) {
	code := ExecCommand(nil, []string{})
	if code != 1 {
		t.Fatalf("expected exit code 1 for empty slice, got %d", code)
	}
}

func TestBuildExecEnvContainsRequiredVars(t *testing.T) {
	envVars := buildExecEnv()

	hasHome := false
	hasPath := false
	hasConstructPath := false
	for _, e := range envVars {
		if e == "HOME=/home/construct" {
			hasHome = true
		}
		if strings.HasPrefix(e, "PATH=/home/") && strings.Contains(e, ".local/bin") {
			hasPath = true
		}
		if strings.HasPrefix(e, "CONSTRUCT_PATH=/home/") && strings.Contains(e, ".local/bin") {
			hasConstructPath = true
		}
	}

	if !hasHome {
		t.Fatal("buildExecEnv missing HOME=/home/construct")
	}
	if !hasPath {
		t.Fatal("buildExecEnv missing PATH")
	}
	if !hasConstructPath {
		t.Fatal("buildExecEnv missing CONSTRUCT_PATH")
	}
}
