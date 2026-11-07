package network

import (
	"os"
	"path/filepath"
	"testing"
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
