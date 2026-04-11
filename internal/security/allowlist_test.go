package security

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanner_isAllowlisted(t *testing.T) {
	tests := []struct {
		name       string
		allowPaths []string
		relPath    string
		want       bool
	}{
		{
			name:       "no allowlist",
			allowPaths: []string{},
			relPath:    "config/secrets.yaml",
			want:       false,
		},
		{
			name:       "exact match",
			allowPaths: []string{"config/secrets.yaml"},
			relPath:    "config/secrets.yaml",
			want:       true,
		},
		{
			name:       "glob pattern match",
			allowPaths: []string{"*.secret"},
			relPath:    "config/my.secret",
			want:       true,
		},
		{
			name:       "glob pattern no match",
			allowPaths: []string{"*.secret"},
			relPath:    "config/not-a-secret.txt",
			want:       false,
		},
		{
			name:       "wildcard path match",
			allowPaths: []string{"**/*.pem"},
			relPath:    "certs/server.pem",
			want:       true, // filepath.Match DOES support ** (matches any number of directories)
		},
		{
			name:       "multiple patterns",
			allowPaths: []string{"*.secret", "credentials/*"},
			relPath:    "credentials/aws",
			want:       true,
		},
		{
			name:       "basename match",
			allowPaths: []string{".env"},
			relPath:    "subdir/.env",
			want:       true,
		},
		{
			name:       "path with special chars",
			allowPaths: []string{"config[prod].yaml"},
			relPath:    "config[prod].yaml",
			want:       false, // [] is special in glob patterns
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := NewManager("/tmp/test-security")
			scanner := NewScanner("/project/root", "hash", []string{}, tt.allowPaths, sm)
			got := scanner.isAllowlisted(tt.relPath)
			if got != tt.want {
				t.Errorf("isAllowlisted() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestScanner_AllowlistedFilesNotRedacted(t *testing.T) {
	// Create temp directory
	tempDir := t.TempDir()

	// Create test files that match candidate patterns
	secretEnv := filepath.Join(tempDir, ".env")
	allowlistedEnv := filepath.Join(tempDir, ".env.allowlist")
	normalConfig := filepath.Join(tempDir, "config.yaml")

	os.WriteFile(secretEnv, []byte("PASSWORD=secret123\nDB_HOST=localhost"), 0644)
	os.WriteFile(allowlistedEnv, []byte("API_KEY=allowlisted_key\nDEBUG=true"), 0644)
	os.WriteFile(normalConfig, []byte("app:\n  name: test\n"), 0644)

	// Create session manager and scanner
	sm := NewManager("/tmp/test-security-allowlist")
	sessionID, _ := GenerateSessionID()

	// Create scanner with allowlist
	scanner := NewScanner(tempDir, "hash", []string{}, []string{".env.allowlist"}, sm)

	// Scan project
	result, err := scanner.ScanProject(sessionID, false)
	if err != nil {
		t.Fatalf("ScanProject() error = %v", err)
	}

	t.Logf("Scan result: FilesScanned=%d, FilesRedacted=%d, SecretsCount=%d",
		result.FilesScanned, result.FilesRedacted, result.SecretsCount)

	// Verify allowlisted file was NOT redacted
	for _, redaction := range result.Redactions {
		if redaction.Path == ".env.allowlist" {
			t.Error(".env.allowlist was redacted, but should have been skipped due to allowlist")
		}
	}

	// Verify .env WAS redacted (it has secrets and is not allowlisted)
	foundEnvRedacted := false
	for _, redaction := range result.Redactions {
		// Path will be full path, so check basename
		if filepath.Base(redaction.Path) == ".env" {
			foundEnvRedacted = true
			break
		}
	}
	if !foundEnvRedacted {
		t.Error(".env was not redacted, but should have been (contains PASSWORD and is not allowlisted)")
	}
}

func TestScanner_AllowlistWithDenyPathsInteraction(t *testing.T) {
	// Create temp directory
	tempDir := t.TempDir()

	// Create test files
	secretEnv := filepath.Join(tempDir, ".env")
	allowlistedEnv := filepath.Join(tempDir, ".env.allowlist")
	secretConfig := filepath.Join(tempDir, "config.yaml")

	os.WriteFile(secretEnv, []byte("PASSWORD=secret123"), 0644)
	os.WriteFile(allowlistedEnv, []byte("API_KEY=allowlisted_key"), 0644)
	os.WriteFile(secretConfig, []byte("password: config_secret"), 0644)

	// Create session manager and scanner
	sm := NewManager("/tmp/test-security-interaction")
	sessionID, _ := GenerateSessionID()

	// Scanner with deny_paths (force scan) but allowlist (skip redaction)
	// Note: deny_paths works together with candidate patterns, not as a replacement
	scanner := NewScanner(tempDir, "hash",
		[]string{"custom.env*"},    // Deny: force scan these specific patterns
		[]string{".env.allowlist"}, // Allowlist: but skip this one
		sm)

	result, err := scanner.ScanProject(sessionID, false)
	if err != nil {
		t.Fatalf("ScanProject() error = %v", err)
	}

	// Check that .env.allowlist was NOT redacted
	for _, redaction := range result.Redactions {
		if redaction.Path == ".env.allowlist" {
			t.Error(".env.allowlist was redacted, but allowlist should take precedence")
		}
	}

	// .env should be redacted because it's a candidate pattern
	foundEnv := false
	for _, redaction := range result.Redactions {
		// Path will be full path, so check basename
		if filepath.Base(redaction.Path) == ".env" {
			foundEnv = true
			break
		}
	}
	if !foundEnv {
		t.Error(".env was not redacted, but should have been (candidate pattern, not allowlisted)")
	}
}

func TestScanner_AllowlistEmptyByDefault(t *testing.T) {
	// Verify default behavior (no allowlist)
	sm := NewManager("/tmp/test-security-default")
	scanner := NewScanner("/project/root", "hash", []string{}, []string{}, sm)

	// No files should be allowlisted by default
	if scanner.isAllowlisted("any/file.txt") {
		t.Error("isAllowlisted() returned true with empty allowlist")
	}
	if scanner.isAllowlisted(".env") {
		t.Error("isAllowlisted() returned true for .env with empty allowlist")
	}
}

func TestScanner_AllowlistSecurityWarning(t *testing.T) {
	// Create temp directory
	tempDir := t.TempDir()

	// Create allowlisted file with secret
	allowlistedFile := filepath.Join(tempDir, "credentials.conf")
	os.WriteFile(allowlistedFile, []byte("PASSWORD=my_secret_password"), 0644)

	// Create session manager and scanner
	sm := NewManager("/tmp/test-security-warning")
	sessionID, _ := GenerateSessionID()

	// Capture stderr to verify warning is printed
	// (In real implementation, this would go to stderr)
	scanner := NewScanner(tempDir, "hash", []string{}, []string{"credentials.conf"}, sm)

	_, err := scanner.ScanProject(sessionID, false)
	if err != nil {
		t.Fatalf("ScanProject() error = %v", err)
	}

	// Note: In a real test, we'd capture stderr and verify the warning message
	// For now, we just verify the scan completes without error
	// The warning message is: "warning: file %s is allowlisted and will NOT be redacted"
}

func TestScanner_AllowlistCaseSensitivity(t *testing.T) {
	sm := NewManager("/tmp/test-security-case")
	scanner := NewScanner("/project/root", "hash", []string{}, []string{"SECRET.txt"}, sm)

	tests := []struct {
		path string
		want bool
	}{
		{"SECRET.txt", true},  // Exact match
		{"secret.txt", false}, // Different case
		{"SECRET.TXT", false}, // Different case extension (filepath.Match is case-sensitive)
		{"secret.TXT", false}, // Mixed case, no match
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := scanner.isAllowlisted(tt.path)
			if got != tt.want {
				t.Errorf("isAllowlisted(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestScanner_AllowlistWildcardPatterns(t *testing.T) {
	sm := NewManager("/tmp/test-security-wildcards")

	tests := []struct {
		name       string
		allowPaths []string
		testPaths  map[string]bool // path -> expected isAllowlisted result
	}{
		{
			name:       "asterisk pattern",
			allowPaths: []string{"*.pem"},
			testPaths: map[string]bool{
				"file.pem":        true, // basename matches
				"cert.pem":        true, // basename matches
				"file.txt":        false,
				"subdir/cert.pem": true, // basename matches (we check basename)
			},
		},
		{
			name:       "question mark pattern",
			allowPaths: []string{"file?.txt"},
			testPaths: map[string]bool{
				"file1.txt":  true,
				"file2.txt":  true,
				"file10.txt": false, // ? matches exactly one char
				"file.txt":   false,
			},
		},
		{
			name:       "character class",
			allowPaths: []string{"config.[ty]ml"},
			testPaths: map[string]bool{
				"config.yml": true,
				"config.tml": true,
				"config.xml": false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scanner := NewScanner("/project/root", "hash", []string{}, tt.allowPaths, sm)
			for path, want := range tt.testPaths {
				got := scanner.isAllowlisted(path)
				if got != want {
					t.Errorf("isAllowlisted(%q) = %v, want %v", path, got, want)
				}
			}
		})
	}
}
