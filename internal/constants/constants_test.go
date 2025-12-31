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

// TestFileBasedPasteAgents ensures that required agents are listed for file-based paste
func TestFileBasedPasteAgents(t *testing.T) {
	required := []string{"gemini", "qwen", "codex"}
	for _, agent := range required {
		if !contains(FileBasedPasteAgents, agent) {
			t.Errorf("FileBasedPasteAgents should contain '%s', got '%s'", agent, FileBasedPasteAgents)
		}
	}
}

func contains(s, substr string) bool {
	parts := split(s, ",")
	for _, p := range parts {
		if p == substr {
			return true
		}
	}
	return false
}

func split(s, sep string) []string {
	var res []string
	start := 0
	for i := 0; i < len(s); i++ {
		if string(s[i]) == sep {
			res = append(res, s[start:i])
			start = i + 1
		}
	}
	res = append(res, s[start:])
	return res
}
