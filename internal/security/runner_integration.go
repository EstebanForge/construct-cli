package security

import (
	"fmt"
	"os"
	"sync"

	"github.com/EstebanForge/construct-cli/internal/config"
)

var (
	globalSession Session
	sessionMutex  sync.RWMutex
)

// InitializeSessionForRun creates and initializes a security session for a run.
// This should be called early in the agent execution flow.
func InitializeSessionForRun(cfg *config.Config, configDir string, projectRoot string) (Session, error) {
	sessionMutex.Lock()
	defer sessionMutex.Unlock()

	// Use the new deep factory
	sess, err := Open(cfg, configDir, projectRoot)
	if err != nil {
		return nil, err
	}

	globalSession = sess
	return sess, nil
}

// GetGlobalSession returns the active session, if any.
func GetGlobalSession() Session {
	sessionMutex.RLock()
	defer sessionMutex.RUnlock()
	return globalSession
}

// CleanupSession cleans up the active session.
func CleanupSession() error {
	sessionMutex.Lock()
	defer sessionMutex.Unlock()

	if globalSession == nil {
		return nil
	}

	err := globalSession.Close()
	globalSession = nil
	return err
}

// GetProjectRoot returns the appropriate project root for the current session.
// If hide-secrets is active, returns the merged overlay directory.
// Otherwise, returns the original project root.
func GetProjectRoot(originalProjectRoot string) string {
	sess := GetGlobalSession()
	if sess == nil {
		return originalProjectRoot
	}
	return sess.ProjectRoot()
}

// IsActive returns true if hide-secrets mode is currently active.
func IsActive() bool {
	sess := GetGlobalSession()
	return sess != nil && sess.IsActive()
}

// GetConfigForEnv returns environment variable overrides for hide-secrets mode.
func GetConfigForEnv() []string {
	if !IsActive() {
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
	sess := GetGlobalSession()
	if sess == nil {
		return environ
	}
	return sess.MaskEnv(environ)
}

// GetMaskedEnvMap returns a masked environment map for agent execution.
func GetMaskedEnvMap(environ []string) map[string]string {
	sess := GetGlobalSession()
	if sess == nil {
		envMap := make(map[string]string)
		for _, env := range environ {
			parts := splitEnv(env)
			if len(parts) == 2 {
				envMap[parts[0]] = parts[1]
			}
		}
		return envMap
	}

	// Re-use MaskEnv but convert to map for agent
	maskedSlice := sess.MaskEnv(environ)
	envMap := make(map[string]string)
	for _, env := range maskedSlice {
		parts := splitEnv(env)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}
	return envMap
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
	sess := GetGlobalSession()
	if sess == nil {
		return ""
	}
	return sess.Report()
}
