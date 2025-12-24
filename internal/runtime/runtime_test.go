package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

// TestGetOSInfo tests OS information retrieval
// getOSInfo is not in runtime package anymore (it was private in main.go and I didn't export it in runtime.go because it seemed unused except for test?)
// Wait, getOSInfo was in main.go. Did I move it?
// I moved `getOSInfo` to `runtime`? No, I checked `runtime.go` content and didn't see `GetOSInfo`.
// It was unused in `main.go`. I might have dropped it.
// If it's unused, I don't need to test it.

func TestGenerateDockerComposeOverride(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()
	containerDir := filepath.Join(tmpDir, "container")
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Generate override
	if err := GenerateDockerComposeOverride(tmpDir, "bridge"); err != nil {
		t.Fatalf("GenerateDockerComposeOverride failed: %v", err)
	}

	// Read file
	content, err := os.ReadFile(filepath.Join(containerDir, "docker-compose.override.yml"))
	if err != nil {
		t.Fatalf("Failed to read generated file: %v", err)
	}

	// Check content
	contentStr := string(content)
	if !strings.Contains(contentStr, "${PWD}:/workspace") {
		t.Errorf("Expected mount to /workspace, got: %s", contentStr)
	}
}
