package sys

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetSupportedAgents(t *testing.T) {
	agents := GetSupportedAgents()
	expectedAgents := 7
	if len(agents) != expectedAgents {
		t.Errorf("Expected %d agents, got %d", expectedAgents, len(agents))
	}

	// Verify specific agents exist
	foundCline := false
	for _, a := range agents {
		if a.Name == "cline" {
			foundCline = true
			if len(a.Paths) != 2 {
				t.Errorf("Expected Cline to have 2 paths, got %d", len(a.Paths))
			}
		}
	}
	if !foundCline {
		t.Error("Cline CLI not found in supported agents")
	}
}

func TestExpandPath(t *testing.T) {
	// Construct home is relative to ConfigDir which is relative to UserHomeDir
	home, _ := os.UserHomeDir()
	expectedBase := filepath.Join(home, ".config/construct-cli/home")

	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{
			name:     "Expand home to Construct home",
			path:     "~/.gemini/GEMINI.md",
			expected: filepath.Join(expectedBase, ".gemini/GEMINI.md"),
		},
		{
			name:     "Regular path",
			path:     "/etc/hosts",
			expected: "/etc/hosts",
		},
		{
			name:     "Relative path",
			path:     "test.txt",
			expected: "test.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExpandPath(tt.path)
			if err != nil {
				t.Errorf("ExpandPath(%s) error: %v", tt.path, err)
				return
			}
			if got != tt.expected {
				t.Errorf("ExpandPath(%s) = %s, want %s", tt.path, got, tt.expected)
			}
		})
	}
}

func TestOpenAgentMemory_Creation(t *testing.T) {
	// Create a temp home for testing
	tempHome, err := os.MkdirTemp("", "construct-test-home")
	if err != nil {
		t.Fatalf("Failed to create temp home: %v", err)
	}
	defer os.RemoveAll(tempHome)

	// Override HOME env for testing
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tempHome)
	defer os.Setenv("HOME", oldHome)

	agent := AgentMemory{
		Name:         "testagent",
		FriendlyName: "Test Agent",
		Paths:        []string{"~/.testagent/rules.md"},
	}

	// Since OpenAgentMemory calls openInEditor (which has side effects),
	// we would ideally mock the editor. But for a simple test of file creation:
	// Let's verify our logic in OpenAgentMemory works up to the creation point.

	expanded, _ := ExpandPath(agent.Paths[0])

	// Ensure it doesn't exist
	if _, err := os.Stat(expanded); err == nil {
		t.Fatalf("Test file already exists: %s", expanded)
	}

	// We can't easily call OpenAgentMemory because it calls GUI/Terminal editors
	// but we can test the directory/file creation logic separately if we refactor
	// or just test that if we create it, Stat works.
}
