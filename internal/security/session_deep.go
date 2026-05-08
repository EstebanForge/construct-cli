package security

import (
	"fmt"

	"github.com/EstebanForge/construct-cli/internal/config"
)

// noOpSession is a session that does nothing, used when hide-secrets is disabled.
type noOpSession struct {
	originalRoot string
}

func (s *noOpSession) ProjectRoot() string               { return s.originalRoot }
func (s *noOpSession) MaskEnv(environ []string) []string { return environ }
func (s *noOpSession) Report() string                    { return "" }
func (s *noOpSession) IsActive() bool                    { return false }
func (s *noOpSession) Close() error                      { return nil }

// secureSession is the implementation of a full hide-secrets session.
type secureSession struct {
	manager *SessionManager
	root    string
	masker  *EnvMasker
}

func (s *secureSession) ProjectRoot() string {
	return s.manager.GetProjectRoot(s.root)
}

func (s *secureSession) MaskEnv(environ []string) []string {
	return s.masker.BuildMaskedEnvSlice(environ)
}

func (s *secureSession) Report() string {
	ws := s.manager.GetWorkspace()
	if ws == nil {
		return ""
	}
	return fmt.Sprintf("hide_secrets session active: workspace=%s mask_style=%s",
		ws.MergedDir, ws.MaskStyle)
}

func (s *secureSession) IsActive() bool {
	return s.manager.IsActive()
}

func (s *secureSession) Close() error {
	return s.manager.Cleanup()
}

// NewNoOpSession returns a no-op security session.
func NewNoOpSession(projectRoot string) Session {
	return &noOpSession{originalRoot: projectRoot}
}

// Open initializes a security session.
// It returns a no-op session if hide-secrets mode is disabled.
func Open(cfg *config.Config, configDir string, projectRoot string) (Session, error) {
	// Create session manager
	sm := NewSessionManager(cfg, configDir)

	// Initialize the session (checks enablement internally)
	if err := sm.Initialize(projectRoot); err != nil {
		return nil, fmt.Errorf("failed to initialize security session: %w", err)
	}

	if !sm.IsActive() {
		return &noOpSession{originalRoot: projectRoot}, nil
	}

	return &secureSession{
		manager: sm,
		root:    projectRoot,
		masker: NewEnvMasker(
			sm.config.Security.HideSecretsMaskStyle,
			sm.config.Security.HideSecretsPassthroughVars,
		),
	}, nil
}
