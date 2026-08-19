package runtime

// Backend conformance test suite (docs/VMs.md §7 Step 3).
//
// Defines the contract both isolation backends (Docker today, microsandbox later)
// must satisfy. Today it exercises the existing Docker/Podman primitives directly;
// after interface extraction (Step 4) it runs unchanged against backend_docker.go
// and later backend_msb.go.
//
// Gate: `CONSTRUCT_CONFORMANCE=1 go test -run TestConformance ./internal/runtime/`
// Requires a running container runtime. Image is configurable via
// CONSTRUCT_CONFORMANCE_IMAGE (default debian:bookworm-slim); pass
// construct-box:latest to test the real image.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const conformanceName = "construct-conformance"

func conformanceRuntime(t *testing.T) string {
	t.Helper()
	if os.Getenv("CONSTRUCT_CONFORMANCE") != "1" {
		t.Skip("set CONFORMANCE=1 to run the backend conformance suite")
	}
	// Probe binaries directly: DetectRuntime has side effects (prints, spawns
	// podman service, can os.Exit) that turn a skip into a test-binary failure
	// inside nested sandboxes.
	for _, name := range []string{"docker", "podman"} {
		if _, err := exec.LookPath(name); err == nil && IsRuntimeRunning(name) {
			rt := name
			t.Cleanup(func() {
				_ = exec.Command(rt, "rm", "-f", conformanceName).Run()
			})
			return rt
		}
	}
	t.Skip("no container runtime available")
	return "" // unreachable; t.Skip calls runtime.Goexit
}

func conformanceImage() string {
	if img := os.Getenv("CONSTRUCT_CONFORMANCE_IMAGE"); img != "" {
		return img
	}
	return "debian:bookworm-slim"
}

// mustRunExec wraps ExecInContainerWithEnv and fails with output on error.
func mustRunExec(t *testing.T, rt, name string, cmdArgs, envVars []string, user string) string {
	t.Helper()
	out, err := ExecInContainerWithEnv(rt, name, cmdArgs, envVars, user)
	if err != nil {
		t.Fatalf("exec %v in %s: %v\noutput: %s", cmdArgs, name, err, out)
	}
	return out
}

// startConformanceContainer launches a labeled, mounted container kept alive
// for the whole suite (daemon replacement stand-in).
func startConformanceContainer(t *testing.T, rt string) (name, hostDir string) {
	t.Helper()
	var err error
	hostDir, err = os.MkdirTemp("", "construct-conformance")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(hostDir) })

	label := DaemonMountsLabelKey + "=conformance"
	cmd := exec.Command(rt, "run", "-d", "--rm",
		"--name", conformanceName,
		"--label", label,
		"-v", hostDir+":/workspace",
		"-w", "/workspace",
		conformanceImage(), "sleep", "300")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("start container: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command(rt, "rm", "-f", conformanceName).Run() })

	// Wait until reported running.
	for i := 0; i < 30; i++ {
		if GetContainerState(rt, conformanceName) == ContainerStateRunning {
			return conformanceName, hostDir
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("container never reached running state: %v", GetContainerState(rt, conformanceName))
	return "", ""
}

func TestConformanceContainerState(t *testing.T) {
	rt := conformanceRuntime(t)
	startConformanceContainer(t, rt)

	if got := GetContainerState(rt, conformanceName); got != ContainerStateRunning {
		t.Errorf("GetContainerState = %v, want %v", got, ContainerStateRunning)
	}
	if !IsContainerRunning(rt, conformanceName) {
		t.Error("IsContainerRunning = false, want true")
	}
	if got := GetContainerState(rt, conformanceName+"-nope"); got != ContainerStateMissing {
		t.Errorf("missing container state = %v, want %v", got, ContainerStateMissing)
	}
}

func TestConformanceInspect(t *testing.T) {
	rt := conformanceRuntime(t)
	_, hostDir := startConformanceContainer(t, rt)

	// Label round-trip (daemon mount labels).
	got, err := GetContainerLabel(rt, conformanceName, DaemonMountsLabelKey)
	if err != nil {
		t.Fatalf("GetContainerLabel: %v", err)
	}
	if got != "conformance" {
		t.Errorf("label = %q, want %q", got, "conformance")
	}

	// Working dir inspection.
	wd, err := GetContainerWorkingDir(rt, conformanceName)
	if err != nil {
		t.Fatalf("GetContainerWorkingDir: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(wd), "/workspace") {
		t.Errorf("working dir = %q, want /workspace", wd)
	}

	// Mount source inspection: host temp dir must map back from /workspace.
	// Compare against both raw and symlink-resolved host paths (macOS /var vs /private/var).
	src, err := GetContainerMountSource(rt, conformanceName, "/workspace")
	if err != nil {
		t.Fatalf("GetContainerMountSource: %v", err)
	}
	src = strings.TrimSpace(src)
	resolved, _ := filepath.EvalSymlinks(hostDir)
	if src != hostDir && src != resolved {
		t.Errorf("mount source = %q, want %q (or resolved %q)", src, hostDir, resolved)
	}
}

func TestConformanceExecEnvOrdering(t *testing.T) {
	rt := conformanceRuntime(t)
	name, _ := startConformanceContainer(t, rt)

	// Ordered env: later assignment of the same var must win (engine.go:623
	// mutates env in place relying on ordered -e semantics).
	envVars := []string{
		"CONFORMANCE_VAL=first",
		"CONFORMANCE_VAL=second",
	}
	out := mustRunExec(t, rt, name, []string{"sh", "-c", "printf %s \"$CONFORMANCE_VAL\""}, envVars, "")
	if strings.TrimSpace(out) != "second" {
		t.Errorf("ordered env: got %q, want %q", out, "second")
	}
}

func TestConformanceExecAsUser(t *testing.T) {
	rt := conformanceRuntime(t)
	name, _ := startConformanceContainer(t, rt)

	// Default exec user.
	out := mustRunExec(t, rt, name, []string{"sh", "-c", "id -u"}, nil, "")
	if strings.TrimSpace(out) != "0" {
		t.Errorf("default exec uid = %q, want 0", out)
	}

	// Named user exec (ResolveExecUser equivalent).
	out = mustRunExec(t, rt, name, []string{"sh", "-c", "id -u"}, nil, "1")
	if strings.TrimSpace(out) != "1" {
		t.Errorf("exec as uid 1 = %q, want 1", out)
	}
}

func TestConformanceExecExitCodes(t *testing.T) {
	rt := conformanceRuntime(t)
	name, _ := startConformanceContainer(t, rt)

	// 127: command not found; 126: not executable. Both must surface as the
	// exec exit code, not collapse into a generic error (engine.go:451 hint).
	cases := []struct {
		desc string
		args []string
		want int
	}{
		{"command not found", []string{"sh", "-c", "exit 127"}, 127},
		{"not executable", []string{"sh", "-c", "exit 126"}, 126},
		{"plain failure", []string{"sh", "-c", "exit 3"}, 3},
		{"success", []string{"sh", "-c", "exit 0"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			code, err := ExecInteractive(rt, name, tc.args, nil, "")
			if err != nil {
				t.Fatalf("ExecInteractive: %v", err)
			}
			if code != tc.want {
				t.Errorf("exit code = %d, want %d", code, tc.want)
			}
		})
	}
}

func TestConformanceStdinHandling(t *testing.T) {
	rt := conformanceRuntime(t)
	name, _ := startConformanceContainer(t, rt)

	// Piped stdin must reach the guest command (msb stdin trap, §7.1).
	cmd := exec.Command(rt, "exec", "-i", name, "sh", "-c", "cat")
	cmd.Stdin = strings.NewReader("stdin-roundtrip")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("stdin exec: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "stdin-roundtrip" {
		t.Errorf("stdin roundtrip = %q", out)
	}
}

func TestConformanceLifecycleStopCleanup(t *testing.T) {
	rt := conformanceRuntime(t)
	startConformanceContainer(t, rt)

	// Stop, then observe exited state, then cleanup (no error on missing).
	if err := StopContainer(rt, conformanceName); err != nil {
		t.Fatalf("StopContainer: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for GetContainerState(rt, conformanceName) == ContainerStateRunning && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	if got := GetContainerState(rt, conformanceName); got == ContainerStateRunning {
		t.Fatalf("container still running after StopContainer")
	}
	if err := CleanupExitedContainer(rt, conformanceName); err != nil {
		t.Fatalf("CleanupExitedContainer: %v", err)
	}
	if got := GetContainerState(rt, conformanceName); got != ContainerStateMissing {
		t.Errorf("state after cleanup = %v, want %v", got, ContainerStateMissing)
	}
}

func TestConformanceCheckImageCommand(t *testing.T) {
	rt := conformanceRuntime(t)
	// GetCheckImageCommand must yield a runnable probe against an image that
	// exists locally (staleness check exec, IsContainerStale support).
	cmdArgs := GetCheckImageCommand(rt)
	if len(cmdArgs) == 0 {
		t.Fatal("GetCheckImageCommand returned empty command")
	}
	// Substitute the image argument with the conformance image if the default
	// construct image is not present locally.
	probe := cmdArgs
	if img := conformanceImage(); img != "" {
		for i, a := range probe {
			if strings.Contains(a, "construct") {
				probe[i] = strings.ReplaceAll(a, "construct-box:latest", img)
			}
		}
	}
	cmd := exec.Command(probe[0], probe[1:]...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("check-image command %v failed: %v\n%s", probe, err, out)
	}
}

func TestConformanceNaming(t *testing.T) {
	rt := conformanceRuntime(t)
	name := CwdContainerName("/tmp/project-x")
	if name == "" {
		t.Fatal("CwdContainerName returned empty")
	}
	_ = rt // naming is backend-independent; rt kept for symmetry
	if !strings.HasPrefix(name, "construct-cli-") {
		t.Errorf("CwdContainerName = %q, want construct-cli- prefix", name)
	}
	// Contract is deterministic hash: same CWD -> same name, different CWD ->
	// different name (dir name is deliberately NOT embedded).
	if again := CwdContainerName("/tmp/project-x"); again != name {
		t.Errorf("CwdContainerName not deterministic: %q vs %q", name, again)
	}
	if other := CwdContainerName("/tmp/project-y"); other == name {
		t.Errorf("CwdContainerName collision between different CWDs: %q", name)
	}
	fmt.Fprintln(os.Stderr, "container name:", name)
}
