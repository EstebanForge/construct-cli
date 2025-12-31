package clipboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogf(t *testing.T) {
	// Setup: use a temporary HOME to avoid polluting real config
	tempHome, err := os.MkdirTemp("", "construct-test-home-*")
	if err != nil {
		t.Fatalf("failed to create temp home: %v", err)
	}
	defer os.RemoveAll(tempHome)

	origHome := os.Getenv("HOME")
	origDebug := os.Getenv("CONSTRUCT_DEBUG")
	os.Setenv("HOME", tempHome)
	defer func() {
		os.Setenv("HOME", origHome)
		os.Setenv("CONSTRUCT_DEBUG", origDebug)
	}()

	logDir := filepath.Join(tempHome, ".config", "construct-cli", "logs")
	logFile := filepath.Join(logDir, "debug_clipboard_server.log")

	t.Run("disabled when CONSTRUCT_DEBUG is not 1", func(t *testing.T) {
		os.Setenv("CONSTRUCT_DEBUG", "0")
		logf("test message %s", "should not appear")

		if _, err := os.Stat(logFile); !os.IsNotExist(err) {
			t.Errorf("log file should not exist when debug is disabled")
		}
	})

	t.Run("enabled when CONSTRUCT_DEBUG is 1", func(t *testing.T) {
		os.Setenv("CONSTRUCT_DEBUG", "1")
		message := "hello debug log"
		logf("test message: %s\n", message)

		content, err := os.ReadFile(logFile)
		if err != nil {
			t.Fatalf("failed to read log file: %v", err)
		}

		if !strings.Contains(string(content), message) {
			t.Errorf("log content mismatch.\nGot: %s\nWant to contain: %s", string(content), message)
		}

		// Verify timestamp presence (simple check for [YYYY-MM-DD HH:MM:SS])
		if !strings.Contains(string(content), "[") || !strings.Contains(string(content), "]") {
			t.Errorf("log should contain timestamp markers")
		}
	})
}
