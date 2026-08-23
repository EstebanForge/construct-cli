package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEvaluateWorkspaceSystemRoots(t *testing.T) {
	roots := []string{"/", "/Users", "/home", "/System", "/private", "/var", "/tmp", "/etc", "/private/etc"}
	for _, root := range roots {
		v := EvaluateWorkspace(root, 100)
		if v.Risk != WorkspaceRiskSystem {
			t.Errorf("expected WorkspaceRiskSystem for %q, got %v", root, v.Risk)
		}
	}
}

func TestEvaluateWorkspaceHomeDirectory(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	v := EvaluateWorkspace(tempHome, 100)
	if v.Risk != WorkspaceRiskHome {
		t.Errorf("expected WorkspaceRiskHome for %q, got %v", tempHome, v.Risk)
	}

	// With AllowHome = false, EnforceWorkspace must reject
	err := EnforceWorkspace(v, WorkspacePolicy{AllowHome: false})
	if err == nil {
		t.Error("expected error when EnforceWorkspace runs on home directory with AllowHome=false")
	}

	// With AllowHome = true, EnforceWorkspace must succeed
	err = EnforceWorkspace(v, WorkspacePolicy{AllowHome: true})
	if err != nil {
		t.Errorf("unexpected error when AllowHome=true: %v", err)
	}
}

func TestEvaluateWorkspaceGitRootBypassesSizePrompt(t *testing.T) {
	tempDir := t.TempDir()
	gitDir := filepath.Join(tempDir, ".git")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create subfiles that would otherwise exceed a small budget
	for i := 0; i < 20; i++ {
		f := filepath.Join(tempDir, "file"+string(rune('a'+i)))
		_ = os.WriteFile(f, []byte("test"), 0o644)
	}

	v := EvaluateWorkspace(tempDir, 5)
	if v.Risk != WorkspaceRiskOK {
		t.Errorf("expected git root to be WorkspaceRiskOK, got %v", v.Risk)
	}
}

func TestEvaluateWorkspaceHotSubtrees(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(tempDir, "Library"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(tempDir, ".cache"), 0o755); err != nil {
		t.Fatal(err)
	}

	v := EvaluateWorkspace(tempDir, 1000)
	if v.Risk != WorkspaceRiskLarge {
		t.Errorf("expected directory with hot subtrees to be WorkspaceRiskLarge, got %v", v.Risk)
	}
}

func TestEvaluateWorkspaceBudgetExceeded(t *testing.T) {
	tempDir := t.TempDir()
	for i := 0; i < 15; i++ {
		f := filepath.Join(tempDir, "file"+string(rune('a'+i)))
		_ = os.WriteFile(f, []byte("test"), 0o644)
	}

	v := EvaluateWorkspace(tempDir, 10)
	if v.Risk != WorkspaceRiskLarge {
		t.Errorf("expected budget exceeded to be WorkspaceRiskLarge, got %v", v.Risk)
	}
	if !v.Capped {
		t.Error("expected Capped to be true")
	}

	// Non-interactive should refuse
	err := EnforceWorkspace(v, WorkspacePolicy{Interactive: false})
	if err == nil {
		t.Error("expected non-interactive EnforceWorkspace to fail for large workspace")
	}

	// Interactive with confirmed=false should cancel
	err = EnforceWorkspace(v, WorkspacePolicy{
		Interactive: true,
		Confirm:     func(string) bool { return false },
	})
	if err == nil {
		t.Error("expected canceled EnforceWorkspace to fail")
	}

	// Interactive with confirmed=true should pass
	err = EnforceWorkspace(v, WorkspacePolicy{
		Interactive: true,
		Confirm:     func(string) bool { return true },
	})
	if err != nil {
		t.Errorf("unexpected error with confirmation: %v", err)
	}
}
