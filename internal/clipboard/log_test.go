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
	logFile := filepath.Join(logDir, "clipboard_server.log")

	t.Run("logf always writes regardless of CONSTRUCT_DEBUG", func(t *testing.T) {
		os.Setenv("CONSTRUCT_DEBUG", "0")
		message := "always-on log message"
		logf("test message: %s\n", message)

		content, err := os.ReadFile(logFile)
		if err != nil {
			t.Fatalf("log file should exist even when CONSTRUCT_DEBUG=0: %v", err)
		}
		if !strings.Contains(string(content), message) {
			t.Errorf("log content mismatch.\nGot: %s\nWant to contain: %s", string(content), message)
		}
		// Verify timestamp presence
		if !strings.Contains(string(content), "[") || !strings.Contains(string(content), "]") {
			t.Errorf("log should contain timestamp markers")
		}
	})

	t.Run("dbgf only writes when CONSTRUCT_DEBUG is 1", func(t *testing.T) {
		// Remove log file so we get a clean state
		os.Remove(logFile)

		os.Setenv("CONSTRUCT_DEBUG", "0")
		dbgf("should not appear: %s\n", "debug-only message")
		if _, err := os.Stat(logFile); !os.IsNotExist(err) {
			t.Errorf("dbgf should not write when CONSTRUCT_DEBUG=0")
		}

		os.Setenv("CONSTRUCT_DEBUG", "1")
		message := "debug detail message"
		dbgf("test debug: %s\n", message)
		content, err := os.ReadFile(logFile)
		if err != nil {
			t.Fatalf("failed to read log file after dbgf: %v", err)
		}
		if !strings.Contains(string(content), message) {
			t.Errorf("dbgf content mismatch.\nGot: %s\nWant to contain: %s", string(content), message)
		}
	})
}
