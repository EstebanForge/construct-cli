package constants

import "testing"

// TestConstants tests application constants
func TestConstants(t *testing.T) {
	if AppName != "construct" {
		t.Errorf("Expected AppName 'construct', got '%s'", AppName)
	}
	if ConfigDir != ".config/construct-cli" {
		t.Errorf("Expected ConfigDir '.config/construct-cli', got '%s'", ConfigDir)
	}
	if ImageName != "construct-box" {
		t.Errorf("Expected ImageName 'construct-box', got '%s'", ImageName)
	}
	if Version == "" {
		t.Error("Version should not be empty")
	}
}
