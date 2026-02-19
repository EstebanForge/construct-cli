package doctor

import (
	"os"
	"path/filepath"
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
