package security

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Scanner performs secret detection and redaction.
type Scanner struct {
	redactor       *Redactor
	denyPaths      []string
	projectRoot    string
	sessionManager *Manager
}

// ScanResult contains statistics from a scan.
type ScanResult struct {
	FilesScanned  int
	FilesRedacted int
	SecretsCount  int
	Redactions    []*FileRedaction
}

// NewScanner creates a new scanner.
func NewScanner(projectRoot string, maskStyle string, denyPaths []string, sessionManager *Manager) *Scanner {
	return &Scanner{
		redactor:       NewRedactor(maskStyle),
		denyPaths:      denyPaths,
		projectRoot:    projectRoot,
		sessionManager: sessionManager,
	}
}

// ScanProject scans the project for secrets and creates redacted copies.
func (s *Scanner) ScanProject(sessionID SessionID, hideGitDir bool) (*ScanResult, error) {
	result := &ScanResult{
		Redactions: []*FileRedaction{},
	}

	err := filepath.WalkDir(s.projectRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if d.IsDir() {
			// Skip .git directory
			if hideGitDir && filepath.Base(path) == ".git" {
				return filepath.SkipDir
			}

			// Skip hard-excluded paths
			if IsHardExcluded(path, s.projectRoot) {
				return filepath.SkipDir
			}

			return nil
		}

		// Check if file should be scanned
		relPath, err := filepath.Rel(s.projectRoot, path)
		if err != nil {
			return err
		}

		shouldScan := s.shouldScanFile(relPath, path)
		if !shouldScan {
			return nil
		}

		result.FilesScanned++

		// Read file content
		content, err := os.ReadFile(path)
		if err != nil {
			// Log error but continue scanning
			fmt.Fprintf(os.Stderr, "warning: failed to read file %s: %v\n", path, err)
			return nil
		}

		// Check if file contains secrets
		redacted, redaction, err := s.redactor.RedactFile(path, content)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to redact file %s: %v\n", path, err)
			return nil
		}

		if redaction.SecretsCount > 0 {
			result.FilesRedacted++
			result.SecretsCount += redaction.SecretsCount
			result.Redactions = append(result.Redactions, redaction)

			// Write redacted copy to session upper layer
			sessionDir := s.sessionManager.GetSessionDir(sessionID)
			upperPath := filepath.Join(sessionDir, "upper", relPath)

			// Create directory structure
			if err := os.MkdirAll(filepath.Dir(upperPath), 0755); err != nil {
				return fmt.Errorf("failed to create upper directory: %w", err)
			}

			// Write redacted content
			if err := os.WriteFile(upperPath, redacted, 0644); err != nil {
				return fmt.Errorf("failed to write redacted file: %w", err)
			}
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("scan failed: %w", err)
	}

	return result, nil
}

// shouldScanFile determines if a file should be scanned for secrets.
func (s *Scanner) shouldScanFile(relPath, fullPath string) bool {
	// Check hard exclusions first
	if IsHardExcluded(fullPath, s.projectRoot) {
		return false
	}

	// Check candidate patterns
	if MatchesCandidatePattern(relPath, s.denyPaths) {
		return true
	}

	// For V1, only scan files that match candidate patterns
	// Content-based scanning would be done here in V2
	return false
}

// CheckSymlinks verifies that symlinks don't escape the project root.
func (s *Scanner) CheckSymlinks(projectRoot string) ([]string, error) {
	var blockedSymlinks []string

	err := filepath.WalkDir(projectRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.Type()&os.ModeSymlink != 0 {
			// Resolve symlink
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}

			// Check if symlink is absolute (escapes project)
			if filepath.IsAbs(target) {
				blockedSymlinks = append(blockedSymlinks, path)
				return nil
			}

			// Resolve relative symlink
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				blockedSymlinks = append(blockedSymlinks, path)
				return nil
			}

			// Check if resolved path is inside project root
			absResolved, err := filepath.Abs(resolved)
			if err != nil {
				blockedSymlinks = append(blockedSymlinks, path)
				return nil
			}

			absProject, err := filepath.Abs(projectRoot)
			if err != nil {
				return err
			}

			// Check if resolved path starts with project root
			if !strings.HasPrefix(absResolved, absProject+string(filepath.Separator)) &&
				absResolved != absProject {
				blockedSymlinks = append(blockedSymlinks, path)
			}
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("symlink check failed: %w", err)
	}

	return blockedSymlinks, nil
}

// ContentScan performs content-based scanning using ripgrep patterns.
func (s *Scanner) ContentScan(_ /* projectRoot */ string, patterns []string) ([]string, error) {
	// For V1, this is a placeholder
	// In V2, we'd integrate with ripgrep for fast content scanning
	_ = patterns // TODO: use patterns in V2
	return []string{}, nil
}

// HashFile computes SHA256 hash of a file.
func HashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

// WriteManifest writes the session manifest file.
func (s *Scanner) WriteManifest(sessionDir string, manifest *Session) error {
	manifestPath := filepath.Join(sessionDir, "manifest.json")

	// For now, we'll use simple JSON encoding
	// In production, use json.Marshal with proper error handling
	var buf bytes.Buffer
	fmt.Fprintf(&buf, `{
  "session_id": "%s",
  "created_at": "%s",
  "host_project_root": "%s",
  "overlay_upper_root": "%s",
  "mask_style": "%s",
  "files_scanned": %d,
  "files_redacted": %d,
  "secrets_redacted": %d,
  "mode": "%s",
  "pid": %d
}`,
		manifest.ID,
		manifest.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		manifest.ProjectRoot,
		manifest.OverlayUpper,
		manifest.MaskStyle,
		manifest.FilesScanned,
		manifest.FilesRedacted,
		manifest.SecretsCount,
		manifest.Mode,
		manifest.PID,
	)

	if err := os.WriteFile(manifestPath, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write manifest: %w", err)
	}

	return nil
}

// WriteRedactionIndex writes the redaction index file.
func (s *Scanner) WriteRedactionIndex(sessionDir string, redactions []*FileRedaction) error {
	indexPath := filepath.Join(sessionDir, "redaction-index.json")

	var buf bytes.Buffer
	buf.WriteString("[\n")

	for i, redaction := range redactions {
		if i > 0 {
			buf.WriteString(",\n")
		}
		fmt.Fprintf(&buf, `  {
    "path": "%s",
    "source_hash": "%s",
    "redacted_hash": "%s",
    "secrets_count": %d
  }`,
			redaction.Path,
			redaction.SourceHash,
			redaction.RedactedHash,
			redaction.SecretsCount,
		)
	}

	buf.WriteString("\n]\n")

	if err := os.WriteFile(indexPath, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write redaction index: %w", err)
	}

	return nil
}

// ReadManifest reads a session manifest file.
func ReadManifest(sessionDir string) (*Session, error) {
	manifestPath := filepath.Join(sessionDir, "manifest.json")

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}

	// For V1, this is a simplified parser
	// In production, use json.Unmarshal
	scanner := bufio.NewScanner(bytes.NewReader(data))
	manifest := &Session{}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.Contains(line, "session_id") {
			// Extract value between quotes
			if start := strings.Index(line, "\""); start != -1 {
				if end := strings.Index(line[start+1:], "\""); end != -1 {
					manifest.ID = SessionID(line[start+1 : start+1+end])
				}
			}
		}
		// Parse other fields similarly...
	}

	return manifest, nil
}

// CheckGitExposed checks if .git directory would be exposed in merged view.
func CheckGitExposed(projectRoot string) bool {
	gitDir := filepath.Join(projectRoot, ".git")
	info, err := os.Stat(gitDir)
	if err != nil {
		return false
	}
	return info.IsDir()
}
