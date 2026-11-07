// Package gitclone provides isolated ephemeral clone environments for zero-trust runs.
package gitclone

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/EstebanForge/construct-cli/internal/hostgit"
)

// Session represents an ephemeral cloned repository session.
type Session struct {
	HostRepoDir string
	CloneDir    string
	BaseCommit  string
}

// CreateSession clones the given git repository into an ephemeral directory without hardlinks.
// It applies any uncommitted diff from the host so the guest sees the current working tree.
func CreateSession(ctx context.Context, hostRepoDir string) (*Session, error) {
	// Verify host directory is inside a git work tree
	checkOut, err := hostgit.Output(ctx, "-C", hostRepoDir, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(string(checkOut)) != "true" {
		return nil, fmt.Errorf("%s is not inside a git work tree: %w", hostRepoDir, err)
	}

	// Resolve the base commit from host repo HEAD
	headOut, err := hostgit.Output(ctx, "-C", hostRepoDir, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve host repo HEAD: %w", err)
	}
	baseCommit := strings.TrimSpace(string(headOut))

	cloneDir, err := os.MkdirTemp("", "construct-clone-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary clone directory: %w", err)
	}

	// Clone without hardlinks to prevent shared storage attacks
	cloneCmd := hostgit.Command(ctx, "clone", "--no-hardlinks", hostRepoDir, cloneDir)
	if out, cloneErr := cloneCmd.CombinedOutput(); cloneErr != nil {
		_ = os.RemoveAll(cloneDir) //nolint:errcheck // best-effort cleanup
		return nil, fmt.Errorf("git clone failed (%s): %w", strings.TrimSpace(string(out)), cloneErr)
	}

	// Extract and apply any uncommitted changes from the host
	diffOut, err := hostgit.Output(ctx, "-C", hostRepoDir, "diff", "HEAD")
	if err == nil && len(diffOut) > 0 {
		applyCmd := hostgit.Command(ctx, "-C", cloneDir, "apply")
		applyCmd.Stdin = bytes.NewReader(diffOut)
		if applyErr := applyCmd.Run(); applyErr == nil {
			_ = hostgit.Command(ctx, "-C", cloneDir, //nolint:errcheck // best-effort commit
				"-c", "user.name=Construct",
				"-c", "user.email=construct@local",
				"commit", "-am", "Initial uncommitted changes from host").Run()
		}
	}

	return &Session{
		HostRepoDir: hostRepoDir,
		CloneDir:    cloneDir,
		BaseCommit:  baseCommit,
	}, nil
}

// Teardown inspects the clone for guest changes, creates a verified git bundle if changed,
// and removes the temporary clone directory.
func (s *Session) Teardown(ctx context.Context) (bundlePath string, hasChanges bool, err error) {
	defer func() {
		_ = os.RemoveAll(s.CloneDir) //nolint:errcheck // teardown cleanup
	}()

	if s.CloneDir == "" {
		return "", false, nil
	}

	// Commit any leftover uncommitted changes made inside the clone
	statusOut, err := hostgit.Output(ctx, "-C", s.CloneDir, "status", "--porcelain")
	if err == nil && len(strings.TrimSpace(string(statusOut))) > 0 {
		_ = hostgit.Command(ctx, "-C", s.CloneDir, //nolint:errcheck // best-effort staging
			"-c", "user.name=Construct",
			"-c", "user.email=construct@local",
			"add", "-A").Run()
		_ = hostgit.Command(ctx, "-C", s.CloneDir, //nolint:errcheck // best-effort commit
			"-c", "user.name=Construct",
			"-c", "user.email=construct@local",
			"commit", "-m", "Agent session changes").Run()
	}

	// Check if any commits were added beyond BaseCommit
	revsOut, revErr := hostgit.Output(ctx, "-C", s.CloneDir, "rev-list", s.BaseCommit+"..HEAD")
	if revErr != nil || len(strings.TrimSpace(string(revsOut))) == 0 {
		return "", false, nil
	}

	// Generate git bundle in host repository with BaseCommit as prerequisite
	bundleFile := filepath.Join(s.HostRepoDir, fmt.Sprintf("construct-patch-%d.bundle", time.Now().Unix()))
	bundleCmd := hostgit.Command(ctx, "-C", s.CloneDir, "bundle", "create", bundleFile, s.BaseCommit+"..HEAD")
	if out, bErr := bundleCmd.CombinedOutput(); bErr != nil {
		return "", false, fmt.Errorf("failed to create git bundle (%s): %w", strings.TrimSpace(string(out)), bErr)
	}

	return bundleFile, true, nil
}
