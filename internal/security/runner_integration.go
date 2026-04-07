package security

import (
	"fmt"
	"os"
	"sync"

	"github.com/EstebanForge/construct-cli/internal/config"
)

var (
	globalSessionManager *SessionManager
	sessionMutex         sync.RWMutex
)

// InitializeSessionForRun creates and initializes a session manager for a run.
// This should be called early in the agent execution flow.
func InitializeSessionForRun(cfg *config.Config, configDir string, projectRoot string) (*SessionManager, error) {
	sessionMutex.Lock()
	defer sessionMutex.Unlock()

	// Create new session manager
	sm := NewSessionManager(cfg, configDir)
	globalSessionManager = sm

	// Initialize the session (no-op if hide-secrets is disabled)
	if err := sm.Initialize(projectRoot); err != nil {
		globalSessionManager = nil
		return nil, fmt.Errorf("failed to initialize security session: %w", err)
	}

	return sm, nil
}

// GetGlobalSessionManager returns the active session manager, if any.
func GetGlobalSessionManager() *SessionManager {
	sessionMutex.RLock()
	defer sessionMutex.RUnlock()
	return globalSessionManager
}

// CleanupSession cleans up the active session.
func CleanupSession() error {
	sessionMutex.Lock()
	defer sessionMutex.Unlock()

	if globalSessionManager == nil {
		return nil
	}

	err := globalSessionManager.Cleanup()
	globalSessionManager = nil
	return err
}

// GetProjectRoot returns the appropriate project root for the current session.
// If hide-secrets is active, returns the merged overlay directory.
// Otherwise, returns the original project root.
func GetProjectRoot(originalProjectRoot string) string {
	sm := GetGlobalSessionManager()
	if sm == nil {
		return originalProjectRoot
	}
	return sm.GetProjectRoot(originalProjectRoot)
}

// IsActive returns true if hide-secrets mode is currently active.
func IsActive() bool {
	sm := GetGlobalSessionManager()
	return sm != nil && sm.IsActive()
}

// GetConfigForEnv returns environment variable overrides for hide-secrets mode.
func GetConfigForEnv() []string {
	sm := GetGlobalSessionManager()
	if sm == nil || !sm.IsActive() {
		return nil
	}

	// Force mount_home=false for security
	return []string{"CONSTRUCT_MOUNT_HOME=false"}
}

// ShouldMaskEnv returns true if we should mask environment variables.
func ShouldMaskEnv() bool {
	return IsActive()
}

// GetMaskedEnvSlice returns a masked environment slice for exec calls.
func GetMaskedEnvSlice(environ []string) []string {
	sm := GetGlobalSessionManager()
	if sm == nil || !sm.IsActive() {
		return environ
	}

	masker := NewEnvMasker(
		sm.config.Security.HideSecretsMaskStyle,
		sm.config.Security.HideSecretsPassthroughVars,
	)

	return masker.BuildMaskedEnvSlice(environ)
}

// GetMaskedEnvMap returns a masked environment map for agent execution.
func GetMaskedEnvMap(environ []string) map[string]string {
	sm := GetGlobalSessionManager()
	if sm == nil || !sm.IsActive() {
		envMap := make(map[string]string)
		for _, env := range environ {
			parts := splitEnv(env)
			if len(parts) == 2 {
				envMap[parts[0]] = parts[1]
			}
		}
		return envMap
	}

	masker := NewEnvMasker(
		sm.config.Security.HideSecretsMaskStyle,
		sm.config.Security.HideSecretsPassthroughVars,
	)

	return masker.BuildMaskedEnvMap(environ)
}

// splitEnv splits an environment variable string into key and value.
func splitEnv(env string) []string {
	for i := 0; i < len(env); i++ {
		if env[i] == '=' {
			return []string{env[:i], env[i+1:]}
		}
	}
	return []string{env}
}

// HandleExit performs cleanup on process exit.
// This should be deferred in the main function.
func HandleExit() {
	if err := CleanupSession(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to cleanup security session: %v\n", err)
	}
}

// GetSessionReport returns a session report if available.
func GetSessionReport() string {
	sm := GetGlobalSessionManager()
	if sm == nil || !sm.IsActive() {
		return ""
	}

	ws := sm.GetWorkspace()
	if ws == nil {
		return ""
	}

	return fmt.Sprintf("hide_secrets session active: workspace=%s mask_style=%s",
		ws.MergedDir, ws.MaskStyle)
}
