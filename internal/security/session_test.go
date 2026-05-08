package security

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EstebanForge/construct-cli/internal/config"
)

func TestSession_DeepInterface(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "construct-security-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	projectRoot := filepath.Join(tempDir, "project")
	if err := os.MkdirAll(projectRoot, 0755); err != nil {
		t.Fatalf("Failed to create project root: %v", err)
	}

	configDir := filepath.Join(tempDir, "config")

	t.Run("noOpSession when disabled", func(t *testing.T) {
		cfg := &config.Config{
			Security: config.SecurityConfig{
				HideSecrets: false,
			},
		}

		sess, err := Open(cfg, configDir, projectRoot)
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer sess.Close()

		if sess.IsActive() {
			t.Error("Expected session to be inactive")
		}

		if sess.ProjectRoot() != projectRoot {
			t.Errorf("Expected ProjectRoot to be %s, got %s", projectRoot, sess.ProjectRoot())
		}

		env := []string{"FOO=bar", "PASSWORD=secret"}
		masked := sess.MaskEnv(env)
		if len(masked) != 2 || masked[1] != "PASSWORD=secret" {
			t.Errorf("Expected no masking, got %v", masked)
		}
	})

	t.Run("secureSession when enabled", func(t *testing.T) {
		// Enable experiment gate
		os.Setenv("CONSTRUCT_EXPERIMENT_HIDE_SECRETS", "1")
		defer os.Unsetenv("CONSTRUCT_EXPERIMENT_HIDE_SECRETS")

		// Force "none" isolation for testing (avoids mount errors)
		os.Setenv("CONSTRUCT_SECURITY_WORKSPACE_TYPE", "none")
		defer os.Unsetenv("CONSTRUCT_SECURITY_WORKSPACE_TYPE")

		cfg := &config.Config{
			Security: config.SecurityConfig{
				HideSecrets:          true,
				HideSecretsMaskStyle: "fixed",
			},
		}

		sess, err := Open(cfg, configDir, projectRoot)
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		defer sess.Close()

		if !sess.IsActive() {
			t.Error("Expected session to be active")
		}

		// In "none" mode, ProjectRoot remains the original root
		if sess.ProjectRoot() != projectRoot {
			t.Errorf("Expected ProjectRoot to be %s, got %s", projectRoot, sess.ProjectRoot())
		}

		env := []string{"FOO=bar", "PASSWORD=secret"}
		masked := sess.MaskEnv(env)
		if len(masked) != 2 || masked[1] == "PASSWORD=secret" {
			t.Errorf("Expected masking, got %v", masked)
		}
		if masked[1] != "PASSWORD=CONSTRUCT_REDACTED" {
			t.Errorf("Expected fixed mask CONSTRUCT_REDACTED, got %s", masked[1])
		}
	})
}
