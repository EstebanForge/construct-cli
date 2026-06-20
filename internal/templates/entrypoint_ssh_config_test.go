package templates

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// extractEnsureSSHConfig pulls the ensure_ssh_config function definition out of
// the embedded entrypoint so it can be executed in isolation. The function ends
// at the first line that is a lone "}" at column 0 (inner "} >> ..." blocks are
// indented, so they do not match).
func extractEnsureSSHConfig(t *testing.T) string {
	t.Helper()
	lines := strings.Split(Entrypoint, "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "ensure_ssh_config() {") {
			start = i
			break
		}
	}
	if start == -1 {
		t.Fatal("ensure_ssh_config not found in entrypoint template")
	}
	for i := start + 1; i < len(lines); i++ {
		if lines[i] == "}" {
			return strings.Join(lines[start:i+1], "\n")
		}
	}
	t.Fatal("could not find end of ensure_ssh_config")
	return ""
}

// runEnsureSSHConfig executes ensure_ssh_config with an isolated HOME and the
// given environment, returning the generated ~/.ssh/config contents.
func runEnsureSSHConfig(t *testing.T, home string, env []string, fn string) string {
	t.Helper()
	// The real entrypoint runs with no `set` flags, so unset vars (e.g. an
	// absent CONSTRUCT_SSH_PIN_IDENTITIES) are valid; mirror that here.
	script := fn + "\nensure_ssh_config\n"
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append([]string{"HOME=" + home}, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ensure_ssh_config failed: %v\noutput: %s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(home, ".ssh", "config"))
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	return string(data)
}

// seedKey writes a fake key file (e.g. "default" or "github.pub") into ~/.ssh.
func seedKey(t *testing.T, home, name string) {
	t.Helper()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, name), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func requireBash(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
}

// TestEntrypointSSHConfigNeverHardcodesIdentityAgent guards the per-session
// isolation fix: a hardcoded IdentityAgent would override SSH_AUTH_SOCK and
// break concurrent sessions, so it must never appear.
func TestEntrypointSSHConfigNeverHardcodesIdentityAgent(t *testing.T) {
	requireBash(t)
	fn := extractEnsureSSHConfig(t)
	home := t.TempDir()

	cfg := runEnsureSSHConfig(t, home, []string{"CONSTRUCT_SSH_PIN_IDENTITIES=github.com=github"}, fn)
	if strings.Contains(cfg, "IdentityAgent") {
		t.Fatalf("config must not hardcode IdentityAgent (breaks per-session SSH_AUTH_SOCK):\n%s", cfg)
	}
}

// TestEntrypointSSHConfigPhysicalKeysOnly verifies the no-agent path: only
// existing on-disk keys are listed, and phantom paths are never emitted.
func TestEntrypointSSHConfigPhysicalKeysOnly(t *testing.T) {
	requireBash(t)
	fn := extractEnsureSSHConfig(t)
	home := t.TempDir()
	seedKey(t, home, "default") // present
	// "personal" intentionally absent.

	cfg := runEnsureSSHConfig(t, home, nil, fn)

	if !strings.Contains(cfg, "IdentityFile ~/.ssh/default") {
		t.Fatalf("expected IdentityFile for existing key 'default':\n%s", cfg)
	}
	if strings.Contains(cfg, "~/.ssh/personal") {
		t.Fatalf("must not list phantom key 'personal' (no file on disk):\n%s", cfg)
	}
}

// TestEntrypointSSHConfigPinPublicKey covers the agent pin: with <key>.pub
// present the host gets IdentitiesOnly + the pubkey to dodge MaxAuthTries.
func TestEntrypointSSHConfigPinPublicKey(t *testing.T) {
	requireBash(t)
	fn := extractEnsureSSHConfig(t)
	home := t.TempDir()
	seedKey(t, home, "github.pub")

	cfg := runEnsureSSHConfig(t, home, []string{"CONSTRUCT_SSH_PIN_IDENTITIES=github.com=github"}, fn)

	for _, want := range []string{
		"Host github.com",
		"HostName github.com",
		"IdentityFile ~/.ssh/github.pub",
		"IdentitiesOnly yes",
	} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("pin block missing %q:\n%s", want, cfg)
		}
	}
}

// TestEntrypointSSHConfigPinAlias covers the three-field alias form for two
// accounts on one service: alias=hostname=keyname.
func TestEntrypointSSHConfigPinAlias(t *testing.T) {
	requireBash(t)
	fn := extractEnsureSSHConfig(t)
	home := t.TempDir()
	seedKey(t, home, "work.pub")

	cfg := runEnsureSSHConfig(t, home, []string{"CONSTRUCT_SSH_PIN_IDENTITIES=github-work=github.com=work"}, fn)

	if !strings.Contains(cfg, "Host github-work") {
		t.Fatalf("expected alias Host block:\n%s", cfg)
	}
	if !strings.Contains(cfg, "HostName github.com") {
		t.Fatalf("alias must resolve to real HostName:\n%s", cfg)
	}
	if !strings.Contains(cfg, "IdentityFile ~/.ssh/work.pub") {
		t.Fatalf("alias must pin the named key:\n%s", cfg)
	}
}

// TestEntrypointSSHConfigPinPhysicalFallback verifies a pin falls back to the
// private key file when no .pub exists (no-agent users).
func TestEntrypointSSHConfigPinPhysicalFallback(t *testing.T) {
	requireBash(t)
	fn := extractEnsureSSHConfig(t)
	home := t.TempDir()
	seedKey(t, home, "github") // private key, no .pub

	cfg := runEnsureSSHConfig(t, home, []string{"CONSTRUCT_SSH_PIN_IDENTITIES=github.com=github"}, fn)

	if !strings.Contains(cfg, "IdentityFile ~/.ssh/github") {
		t.Fatalf("expected physical key fallback:\n%s", cfg)
	}
	if strings.Contains(cfg, "github.pub") {
		t.Fatalf("must not reference a non-existent .pub:\n%s", cfg)
	}
}

// TestEntrypointSSHConfigPinSkippedWhenNoKey verifies a pin with no matching key
// file is dropped rather than producing a broken Host block.
func TestEntrypointSSHConfigPinSkippedWhenNoKey(t *testing.T) {
	requireBash(t)
	fn := extractEnsureSSHConfig(t)
	home := t.TempDir()
	// No key files seeded.

	cfg := runEnsureSSHConfig(t, home, []string{"CONSTRUCT_SSH_PIN_IDENTITIES=github.com=ghost"}, fn)

	if strings.Contains(cfg, "Host github.com") {
		t.Fatalf("pin must be skipped when no key file exists:\n%s", cfg)
	}
}

// TestEntrypointSSHConfigRespectsOptOut verifies a user-managed config marked
// "construct-managed: false" is left untouched.
func TestEntrypointSSHConfigRespectsOptOut(t *testing.T) {
	requireBash(t)
	fn := extractEnsureSSHConfig(t)
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	custom := "# construct-managed: false\nHost mine\n  User me\n"
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := runEnsureSSHConfig(t, home, []string{"CONSTRUCT_SSH_PIN_IDENTITIES=github.com=github"}, fn)
	if cfg != custom {
		t.Fatalf("opt-out config was modified:\n%s", cfg)
	}
}
