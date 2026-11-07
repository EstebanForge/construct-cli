package network

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EstebanForge/construct-cli/internal/config"
)

func TestExplain(t *testing.T) {
	t.Helper()
	// Should not panic on valid or invalid destinations
	Explain("api.anthropic.com:443")
	Explain("registry.npmjs.org")
	Explain("invalid-domain-test.local")
}

func TestShowLog(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// Non-existent log file should not panic or fail
	ShowLog([]string{"--tail", "10"})

	// Write sample proxy.log
	logDir := filepath.Join(tmpDir, ".config", "construct-cli", "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatal(err)
	}
	sampleLog := `2026-11-07T16:00:00Z action=ALLOW dest=api.anthropic.com:443 ip=104.18.2.1
2026-11-07T16:00:01Z action=BLOCK dest=evil.com:443 reason="host not in allowlist"
2026-11-07T16:00:02Z action=LEAK_DROP dest=api.anthropic.com:443 reason="unswapped secret bytes"
2026-11-07T16:00:03Z action=FRONTING_DROP dest=evil.com:443 reason="domain fronting"
`
	if err := os.WriteFile(filepath.Join(logDir, "proxy.log"), []byte(sampleLog), 0644); err != nil {
		t.Fatal(err)
	}

	// Read with tail
	ShowLog([]string{"--tail", "2"})
	ShowLog(nil)
}

func TestValidateTarget(t *testing.T) {
	tests := []struct {
		target   string
		wantType string
		wantErr  bool
	}{
		{"api.anthropic.com", "domain", false},
		{"*.github.com", "domain", false},
		{"192.168.1.1", "ip", false},
		{"10.0.0.0/8", "cidr", false},
		{"postgres.internal:5432", "tcp", false},
		{"10.0.0.5:6379", "tcp", false},
		{"*.rds.amazonaws.com:5432", "tcp", false},
		{"postgres.internal:99999", "", true},
		{"postgres.internal:0", "", true},
		{"invalid target with spaces", "", true},
	}

	for _, tc := range tests {
		gotType, err := ValidateTarget(tc.target)
		if (err != nil) != tc.wantErr {
			t.Errorf("ValidateTarget(%q) err = %v, wantErr %v", tc.target, err, tc.wantErr)
		}
		if gotType != tc.wantType {
			t.Errorf("ValidateTarget(%q) = %q, want %q", tc.target, gotType, tc.wantType)
		}
	}
}

func TestTCPRuleConfigLifecycle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.DefaultConfig()
	target := "postgres.internal:5432"

	// 1. Add TCP rule
	if err := AddRuleToConfig(&cfg, target, "allow", "tcp"); err != nil {
		t.Fatalf("AddRuleToConfig failed: %v", err)
	}
	if len(cfg.Network.AllowedTCP) != 1 || cfg.Network.AllowedTCP[0] != target {
		t.Fatalf("expected allowed_tcp to contain %q, got %v", target, cfg.Network.AllowedTCP)
	}

	// 2. Conflict check
	if err := CheckRuleConflicts(&cfg, target, "allow"); err == nil {
		t.Fatalf("expected conflict error when re-adding existing allowed TCP rule")
	}

	// 3. Blocking specific TCP port should be rejected
	if err := AddRuleToConfig(&cfg, target, "block", "tcp"); err == nil {
		t.Fatalf("expected error when attempting to block specific TCP target")
	}

	// 4. Remove rule
	if err := RemoveRuleFromConfig(&cfg, target); err != nil {
		t.Fatalf("RemoveRuleFromConfig failed: %v", err)
	}
	if len(cfg.Network.AllowedTCP) != 0 {
		t.Fatalf("expected allowed_tcp to be empty, got %v", cfg.Network.AllowedTCP)
	}
}
