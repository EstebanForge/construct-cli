package runtime

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/templates"
)

// containerLiveMu serializes the container gates: podman's aardvark-dns
// races concurrent network creation (UDP 53 bind on the bridge address
// fails with EADDRNOTAVAIL), and the msb tier already owns a shared
// engine on this machine.
var containerLiveMu sync.Mutex

// Live gates for the OCI container backend (containers/podman/docker),
// mirroring the msb tier: real engine, real image, real compose path.
// Gated behind CONSTRUCT_CONTAINER_LIVE=1 (make test-containers) and
// skipped when the construct-box image is absent from the engine store.

// containerLiveRuntime skips unless the tier is enabled and an engine
// binary exists, returning the engine the product would auto-pick.
func containerLiveRuntime(t *testing.T) string {
	t.Helper()
	if os.Getenv("CONSTRUCT_CONTAINER_LIVE") != "1" {
		t.Skip("CONSTRUCT_CONTAINER_LIVE not set; live container gates skipped")
	}
	for _, cand := range []string{"podman", "docker"} {
		if _, err := exec.LookPath(cand); err == nil {
			return cand
		}
	}
	t.Skip("no podman or docker binary on PATH")
	return ""
}

// containerLiveSetup isolates HOME, materializes the compose base, and
// returns the engine plus the isolated config dir. The container tier
// only writes small config files under HOME, so t.TempDir() is enough
// scratch (the msb tier needs /var/tmp for its multi-GB store; this one
// does not).
func containerLiveSetup(t *testing.T) (rt string, configPath string) {
	t.Helper()
	// Podman's aardvark-dns races concurrent network creation (UDP 53 bind
	// on the bridge address fails), so the gates serialize.
	containerLiveMu.Lock()
	t.Cleanup(containerLiveMu.Unlock)
	rt = containerLiveRuntime(t)
	realHome := os.Getenv("HOME")
	t.Setenv("HOME", t.TempDir())
	// Rootless podman resolves its graph root from XDG_DATA_HOME (default
	// $HOME/.local/share); without this pin the scratch HOME forks an
	// empty 11.5 GB image store and every gate skips.
	t.Setenv("XDG_DATA_HOME", filepath.Join(realHome, ".local", "share"))
	configPath = config.GetConfigDir()
	containerDir := filepath.Join(configPath, "container")
	if err := os.MkdirAll(containerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(containerDir, "docker-compose.yml")
	if err := os.WriteFile(base, []byte(templates.DockerCompose), 0o644); err != nil {
		t.Fatal(err)
	}
	// The guest's sudo init writes subuid-owned files into the home bind;
	// remap through the userns so t.TempDir can unlink them. Inside podman
	// unshare the host user IS uid 0 (chown 1000 there maps to a host
	// subuid and locks the tree away from the real user).
	scratchHome := t.TempDir()
	t.Cleanup(func() {
		if rt != "podman" {
			return
		}
		_ = exec.Command(rt, "unshare", "chown", "-R", "0:0",
			filepath.Join(scratchHome, ".config", "construct-cli"),
		).Run()
	})
	t.Setenv("HOME", scratchHome)
	return rt, configPath
}

func requireConstructBoxImage(t *testing.T, rt string) {
	t.Helper()
	if err := exec.Command(rt, "image", "inspect", "construct-box:latest").Run(); err != nil {
		t.Skipf("construct-box image not present in the %s store; run make test-containers once", rt)
	}
}

// TestContainerLiveComposeRunForeground boots a real container through
// the product's compose path (base + generated override) and proves the
// project bind mounts and the Linux user mapping work: the guest reads
// a host-written marker and reports the construct user.
func TestContainerLiveComposeRunForeground(t *testing.T) {
	rt, configPath := containerLiveSetup(t)
	requireConstructBoxImage(t, rt)
	cfg := config.DefaultConfig()

	proj := filepath.Join(t.TempDir(), "projm")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := "CT_E2E_MARKER_" + fmt.Sprintf("%d", time.Now().UnixNano())
	if err := os.WriteFile(filepath.Join(proj, "marker.txt"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	guestPath := getProjectMountPathFromDir(proj)

	if err := GenerateDockerComposeOverride(configPath, guestPath, cfg.Network.Mode, rt); err != nil {
		t.Fatalf("generate compose override: %v", err)
	}

	osEnv := os.Environ()
	osEnv = append(osEnv, "PWD="+proj, "CONSTRUCT_PROJECT_PATH="+guestPath)

	cmd, err := BuildComposeCommand(rt, configPath, "run", []string{
		"--rm", "construct-box", "/bin/bash", "-lc",
		"cat /projects/projm/marker.txt && whoami",
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd.Dir = filepath.Join(configPath, "container")
	cmd.Env = osEnv
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("compose run: %v\noutput:\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), marker) {
		t.Errorf("project marker not visible in guest; output:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "construct") {
		t.Errorf("guest user is not construct; output:\n%s", out.String())
	}
}

// TestContainerLiveDaemonExecTeardown exercises the daemon shape: a
// detached compose run stays up, the engine's exec path delivers output,
// and cleanup removes the container (state must go back to missing).
func TestContainerLiveDaemonExecTeardown(t *testing.T) {
	rt, configPath := containerLiveSetup(t)
	requireConstructBoxImage(t, rt)
	cfg := config.DefaultConfig()

	proj := filepath.Join(t.TempDir(), "projd")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	guestPath := getProjectMountPathFromDir(proj)
	if err := GenerateDockerComposeOverride(configPath, guestPath, cfg.Network.Mode, rt); err != nil {
		t.Fatalf("generate compose override: %v", err)
	}

	name := fmt.Sprintf("ct-e2e-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = CleanupExitedContainer(rt, name)
	})

	osEnv := os.Environ()
	osEnv = append(osEnv, "PWD="+proj, "CONSTRUCT_PROJECT_PATH="+guestPath)

	run, err := BuildComposeCommand(rt, configPath, "run", []string{
		"-d", "--rm", "--name", name, "construct-box",
		"/bin/bash", "-lc", "sleep 120",
	})
	if err != nil {
		t.Fatal(err)
	}
	run.Dir = filepath.Join(configPath, "container")
	run.Env = osEnv
	var runOut bytes.Buffer
	run.Stdout = &runOut
	run.Stderr = &runOut
	if err := run.Run(); err != nil {
		t.Fatalf("detached compose run: %v\noutput:\n%s", err, runOut.String())
	}

	deadline := time.Now().Add(60 * time.Second)
	for GetContainerState(rt, name) != ContainerStateRunning {
		if time.Now().After(deadline) {
			t.Fatalf("container %s never reached running state", name)
		}
		time.Sleep(500 * time.Millisecond)
	}

	execCmd := exec.Command(rt, "exec", name, "/bin/bash", "-lc", "echo CT_E2E_EXEC_OK")
	var execOut bytes.Buffer
	execCmd.Stdout = &execOut
	execCmd.Stderr = &execOut
	if err := execCmd.Run(); err != nil {
		t.Fatalf("guest exec: %v\noutput:\n%s", err, execOut.String())
	}
	if !strings.Contains(execOut.String(), "CT_E2E_EXEC_OK") {
		t.Errorf("exec output missing marker; got:\n%s", execOut.String())
	}

	if err := CleanupExitedContainer(rt, name); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	if state := GetContainerState(rt, name); state != ContainerStateMissing {
		t.Errorf("container still present after teardown: %s", state)
	}
}
