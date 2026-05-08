package security

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"time"
)

// SessionID is a unique identifier for a hide-secrets session.
type SessionID string

// SessionState represents a single hide-secrets session's metadata.
type SessionState struct {
	ID            SessionID `json:"session_id"`
	CreatedAt     time.Time `json:"created_at"`
	ProjectRoot   string    `json:"host_project_root"`
	OverlayUpper  string    `json:"overlay_upper_root"`
	OverlayWork   string    `json:"overlay_work_dir"`
	MaskStyle     string    `json:"mask_style"`
	FilesScanned  int       `json:"files_scanned"`
	FilesRedacted int       `json:"files_redacted"`
	SecretsCount  int       `json:"secrets_redacted"`
	Mode          string    `json:"mode"` // "run" or "daemon"
	PID           int       `json:"pid"`
}

// Session defines the deep interface for a security session lifecycle.
type Session interface {
	// ProjectRoot returns the security-aware path for the project.
	ProjectRoot() string

	// MaskEnv redacts secrets from an environment variable slice.
	MaskEnv(environ []string) []string

	// Report returns a summary of the session findings.
	Report() string

	// IsActive returns true if security isolation is active.
	IsActive() bool

	// Close terminates the session and cleans up resources.
	Close() error
}

// Manager manages hide-secrets sessions.
type Manager struct {
	rootDir string
}

// NewManager creates a new session manager.
func NewManager(configDir string) *Manager {
	securityDir := filepath.Join(configDir, "security")
	return &Manager{
		rootDir: securityDir,
	}
}

// GenerateSessionID creates a new unique session ID.
func GenerateSessionID() (SessionID, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate session ID: %w", err)
	}
	return SessionID(hex.EncodeToString(b)), nil
}

// GetSessionDir returns the directory for the given session ID.
func (m *Manager) GetSessionDir(id SessionID) string {
	return filepath.Join(m.rootDir, "sessions", string(id))
}

// GetAuditLogPath returns the path to the audit log.
func (m *Manager) GetAuditLogPath() string {
	return filepath.Join(m.rootDir, "audit", "security-audit.log")
}

// GetAuditStatePath returns the path to the audit chain state.
func (m *Manager) GetAuditStatePath() string {
	return filepath.Join(m.rootDir, "audit", "security-audit.state")
}
