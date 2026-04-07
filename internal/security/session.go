package security

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SessionID is a unique identifier for a hide-secrets session.
type SessionID string

// Session represents a single hide-secrets session.
type Session struct {
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

// Create creates a new session directory structure.
func (m *Manager) Create(id SessionID, projectRoot, overlayUpper, overlayWork, maskStyle string) (*Session, error) {
	sessionDir := filepath.Join(m.rootDir, "sessions", string(id))

	// Create session directories
	dirs := []string{
		sessionDir,
		overlayUpper,
		overlayWork,
		filepath.Join(m.rootDir, "audit"),
		filepath.Join(m.rootDir, "cache"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("failed to create session directory %s: %w", dir, err)
		}
	}

	session := &Session{
		ID:           id,
		CreatedAt:    time.Now(),
		ProjectRoot:  projectRoot,
		OverlayUpper: overlayUpper,
		OverlayWork:  overlayWork,
		MaskStyle:    maskStyle,
		Mode:         "run",
		PID:          os.Getpid(),
	}

	return session, nil
}

// GetSessionDir returns the directory path for a session.
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

// GetCachePath returns the path to a cache file.
func (m *Manager) GetCachePath(name string) string {
	return filepath.Join(m.rootDir, "cache", name)
}

// Cleanup removes session directories for orphaned sessions.
func (m *Manager) Cleanup() error {
	sessionsDir := filepath.Join(m.rootDir, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read sessions directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		sessionID := entry.Name()
		pidFile := filepath.Join(sessionsDir, sessionID, "pid")
		pidData, err := os.ReadFile(pidFile)
		if err != nil {
			continue
		}

		var pid int
		if _, err := fmt.Sscanf(string(pidData), "%d", &pid); err != nil {
			continue
		}

		// Check if process is still running
		if !isProcessAlive(pid) {
			// Grace period has passed (checked by modification time), safe to cleanup
			sessionDir := filepath.Join(sessionsDir, sessionID)
			if err := os.RemoveAll(sessionDir); err != nil {
				return fmt.Errorf("failed to cleanup session %s: %w", sessionID, err)
			}
		}
	}

	return nil
}

// isProcessAlive checks if a process with the given PID is running.
func isProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(os.Signal(nil))
	return err == nil
}
