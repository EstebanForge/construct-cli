package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCopyBackRefusesSymlink: the staged file lives in the guest-writable
// home bind, so a prompt-injected agent can replace it with a symlink to
// any host file. The teardown copy-back must refuse the link instead of
// copying the target's bytes out of the bind.
func TestCopyBackRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret")
	if err := os.WriteFile(secret, []byte("host-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, "staged.session")
	if err := os.Symlink(secret, staged); err != nil {
		t.Fatal(err)
	}
	hostFile := filepath.Join(dir, "host.session")
	if err := copyFileBestEffort(staged, hostFile); err == nil {
		t.Fatal("guest-planted symlink at the staged path must be refused")
	}
	if _, err := os.Stat(hostFile); !os.IsNotExist(err) {
		t.Fatal("refused copy-back must not create the host file")
	}
}

// TestCopyBackRejectsOversize: a guest-grown staged file must not fill the
// host disk at teardown.
func TestCopyBackRejectsOversize(t *testing.T) {
	dir := t.TempDir()
	staged := filepath.Join(dir, "staged.session")
	if err := os.WriteFile(staged, make([]byte, copyBackLimit+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyFileBestEffort(staged, filepath.Join(dir, "host.session")); err == nil {
		t.Fatal("oversize staged file must be refused")
	}
}
