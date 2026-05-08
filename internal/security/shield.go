package security

import (
	"fmt"

	"github.com/EstebanForge/construct-cli/internal/config"
)

// SecretShield is a deep module that encapsulates secret detection and redaction.
type SecretShield struct {
	scanner  *Scanner
	redactor *Redactor
	cfg      *config.Config
}

// NewSecretShield creates a new secret shield.
func NewSecretShield(cfg *config.Config, projectRoot string, sessionManager *Manager) *SecretShield {
	return &SecretShield{
		cfg:      cfg,
		redactor: NewRedactor(cfg.Security.HideSecretsMaskStyle),
		scanner: NewScanner(
			projectRoot,
			cfg.Security.HideSecretsMaskStyle,
			cfg.Security.HideSecretsDenyPaths,
			cfg.Security.HideSecretsAllowPaths,
			sessionManager,
		),
	}
}

// Protect scans the project and creates redacted copies in the session workspace.
// It returns a consolidated result of the protection operation.
func (s *SecretShield) Protect(sessionID SessionID, hideGitDir bool) (*ScanResult, error) {
	// Consolidate fragmented steps into a single atomic operation
	result, err := s.scanner.ScanProject(sessionID, hideGitDir)
	if err != nil {
		return nil, fmt.Errorf("shield protection failed: %w", err)
	}

	return result, nil
}
