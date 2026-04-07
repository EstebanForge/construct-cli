package security

import (
	"fmt"
	"os"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// SessionManager manages the hide-secrets mode session lifecycle.
type SessionManager struct {
	config         *config.Config
	configDir      string
	status         *EnablementStatus
	workspace      *SessionWorkspace
	scanResult     *ScanResult
	sessionManager *Manager
	wsManager      *WorkspaceManager
	scanner        *Scanner
}

// NewSessionManager creates a new session manager.
func NewSessionManager(cfg *config.Config, configDir string) *SessionManager {
	return &SessionManager{
		config:         cfg,
		configDir:      configDir,
		sessionManager: NewManager(configDir),
		wsManager:      NewWorkspaceManager(configDir),
	}
}

// Initialize checks if hide-secrets mode should be enabled and prepares the session.
func (sm *SessionManager) Initialize(projectRoot string) error {
	// Check enablement
	sm.status = ResolveEnablement(sm.config.Security.HideSecrets)
	LogStartupStatus(sm.status)

	if !sm.status.IsEnabled() {
		// Feature is disabled, no action needed
		return nil
	}

	// Feature is enabled - validate prerequisites
	if err := sm.validatePrerequisites(); err != nil {
		return fmt.Errorf("hide-secrets mode validation failed: %w", err)
	}

	// Force mount_home off as per proposal
	if sm.config.Sandbox.MountHome {
		ui.GumWarning("hide_secrets mode forces mount_home=false for security")
		sm.config.Sandbox.MountHome = false
	}

	// Check for .git directory
	if sm.config.Security.HideGitDir && CheckGitExposed(projectRoot) {
		// .git will be hidden in the merged view
		if ui.CurrentLogLevel >= ui.LogLevelInfo {
			fmt.Println("hide_secrets: .git directory will be hidden in merged view")
		}
	}

	// Generate session ID
	sessionID, err := GenerateSessionID()
	if err != nil {
		return fmt.Errorf("failed to generate session ID: %w", err)
	}

	// Create workspace
	workspace, err := sm.wsManager.CreateSessionWorkspace(sessionID, projectRoot, sm.config.Security.HideSecretsMaskStyle)
	if err != nil {
		return fmt.Errorf("failed to create workspace: %w", err)
	}
	sm.workspace = workspace

	// Create scanner and scan project
	sm.scanner = NewScanner(projectRoot, sm.config.Security.HideSecretsMaskStyle, sm.config.Security.HideSecretsDenyPaths, sm.sessionManager)

	scanResult, err := sm.scanner.ScanProject(sessionID, sm.config.Security.HideGitDir)
	if err != nil {
		// Cleanup workspace on scan failure
		if cerr := sm.workspace.Cleanup(); cerr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to cleanup workspace after scan failure: %v\n", cerr)
		}
		return fmt.Errorf("failed to scan project: %w", err)
	}
	sm.scanResult = scanResult

	// Check for blocked symlinks
	blockedSymlinks, err := sm.scanner.CheckSymlinks(projectRoot)
	if err != nil {
		ui.GumWarning(fmt.Sprintf("Failed to check symlinks: %v", err))
	} else if len(blockedSymlinks) > 0 {
		for _, symlink := range blockedSymlinks {
			fmt.Fprintf(os.Stderr, "warning: blocked symlink: %s (escapes project root)\n", symlink)
		}
	}

	// Write session manifest and redaction index
	sessionDir := sm.sessionManager.GetSessionDir(sessionID)
	if err := sm.scanner.WriteManifest(sessionDir, &Session{
		ID:            sessionID,
		ProjectRoot:   projectRoot,
		OverlayUpper:  workspace.UpperDir,
		OverlayWork:   workspace.WorkDir,
		MaskStyle:     sm.config.Security.HideSecretsMaskStyle,
		FilesScanned:  scanResult.FilesScanned,
		FilesRedacted: scanResult.FilesRedacted,
		SecretsCount:  scanResult.SecretsCount,
		Mode:          "run",
	}); err != nil {
		ui.GumWarning(fmt.Sprintf("Failed to write session manifest: %v", err))
	}

	if err := sm.scanner.WriteRedactionIndex(sessionDir, scanResult.Redactions); err != nil {
		ui.GumWarning(fmt.Sprintf("Failed to write redaction index: %v", err))
	}

	// Emit session report if enabled
	if sm.config.Security.HideSecretsReport {
		sm.emitReport(scanResult)
	}

	return nil
}

// validatePrerequisites checks if the system supports hide-secrets mode.
func (sm *SessionManager) validatePrerequisites() error {
	wsType := DetectWorkspaceType()

	switch wsType {
	case WorkspaceTypeOverlayFS:
		if !IsOverlayFSSupported() {
			return fmt.Errorf("overlayfs not available on this system")
		}
	case WorkspaceTypeAPFSClone:
		if !IsAPFSCloneSupported() {
			return fmt.Errorf("APFS clones not available on this system")
		}
	}

	return nil
}

// emitReport prints the session report.
func (sm *SessionManager) emitReport(result *ScanResult) {
	fmt.Println("\n=== Secret Redaction Session Report ===")
	fmt.Printf("Files scanned: %d\n", result.FilesScanned)
	fmt.Printf("Files redacted: %d\n", result.FilesRedacted)
	fmt.Printf("Secrets redacted: %d\n", result.SecretsCount)
	fmt.Printf("Mask style: %s\n", sm.config.Security.HideSecretsMaskStyle)
	fmt.Printf("Session directory: %s\n", sm.workspace.MergedDir)
	fmt.Println("=====================================")
}

// GetWorkspace returns the prepared workspace (nil if hide-secrets is disabled).
func (sm *SessionManager) GetWorkspace() *SessionWorkspace {
	if !sm.status.IsEnabled() {
		return nil
	}
	return sm.workspace
}

// Cleanup performs session cleanup.
func (sm *SessionManager) Cleanup() error {
	if !sm.status.IsEnabled() {
		return nil
	}

	// Check if workspace has changes
	hasChanges, err := sm.workspace.HasChanges()
	if err != nil {
		return fmt.Errorf("failed to check for changes: %w", err)
	}

	if hasChanges {
		changes, err := sm.workspace.GetChanges()
		if err != nil {
			return fmt.Errorf("failed to get changes: %w", err)
		}

		fmt.Printf("\n=== Hide-Secrets Session Summary ===\n")
		fmt.Printf("Workspace has %d modified file(s)\n", len(changes))
		fmt.Printf("Session directory: %s\n", sm.workspace.MergedDir)
		fmt.Printf("\nNo changes were written to the real project in hide-secrets mode.\n")
		fmt.Printf("Review changes in: %s\n\n", sm.workspace.UpperDir)

		// Keep session artifacts for manual review
		return nil
	}

	// No changes - cleanup workspace
	if err := sm.workspace.Cleanup(); err != nil {
		return fmt.Errorf("failed to cleanup workspace: %w", err)
	}

	return nil
}

// IsActive returns true if hide-secrets mode is active.
func (sm *SessionManager) IsActive() bool {
	return sm.status != nil && sm.status.IsEnabled()
}

// GetProjectRoot returns the appropriate project root for the current session.
// If hide-secrets is active, returns the merged overlay directory.
// Otherwise, returns the original project root.
func (sm *SessionManager) GetProjectRoot(originalProjectRoot string) string {
	if sm.IsActive() && sm.workspace != nil {
		return sm.workspace.MergedDir
	}
	return originalProjectRoot
}
