package hostgit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeEnv(t *testing.T) {
	input := []string{
		"GIT_DIR=/tmp/evil",
		"GIT_CONFIG_PARAMETERS=something",
		"USER=esteban",
		"PATH=/bin:./relative:/usr/bin",
	}

	sanitized := sanitizeEnv(input)
	envMap := make(map[string]string)
	for _, e := range sanitized {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	if _, found := envMap["GIT_DIR"]; found {
		t.Errorf("GIT_DIR was not stripped")
	}
	if _, found := envMap["GIT_CONFIG_PARAMETERS"]; found {
		t.Errorf("GIT_CONFIG_PARAMETERS was not stripped")
	}
	if envMap["USER"] != "esteban" {
		t.Errorf("expected USER=esteban, got %q", envMap["USER"])
	}

	pathParts := filepath.SplitList(envMap["PATH"])
	for _, p := range pathParts {
		if !filepath.IsAbs(p) {
			t.Errorf("relative path %q found in sanitized PATH", p)
		}
	}
}

func TestHardenedCommandEnv(t *testing.T) {
	ctx := context.Background()
	cmd := Command(ctx, "status")

	hasKeyVal := func(key, val string) bool {
		target := key + "=" + val
		for _, e := range cmd.Env {
			if e == target {
				return true
			}
		}
		return false
	}

	required := map[string]string{
		"GIT_TERMINAL_PROMPT": "0",
		"GIT_NO_LAZY_FETCH":   "1",
		"GIT_ALLOW_PROTOCOL":  "file:ssh",
		"GIT_SSH_COMMAND":     "false",
		"GIT_SSH_VARIANT":     "ssh",
	}

	for k, v := range required {
		if !hasKeyVal(k, v) {
			t.Errorf("missing required env %s=%s", k, v)
		}
	}

	foundHooksPath := false
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "GIT_CONFIG_VALUE_") && strings.Contains(e, os.DevNull) {
			foundHooksPath = true
			break
		}
	}
	if !foundHooksPath {
		t.Errorf("did not find core.hooksPath=%s in git config values", os.DevNull)
	}
}

func TestOutputVersion(t *testing.T) {
	ctx := context.Background()
	out, err := Output(ctx, "version")
	if err != nil {
		t.Fatalf("failed to run git version: %v", err)
	}
	if !strings.HasPrefix(string(out), "git version") {
		t.Errorf("unexpected output: %s", string(out))
	}
}
