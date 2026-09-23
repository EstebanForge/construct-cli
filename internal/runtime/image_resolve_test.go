package runtime

import (
	"strings"
	"testing"
)

func withAcquisitionSeams(t *testing.T, tty bool, confirm bool) {
	t.Helper()
	origTTY, origConfirm := stdinIsTerminal, confirmPrompt
	stdinIsTerminal = func() bool { return tty }
	confirmPrompt = func(string) bool { return confirm }
	t.Cleanup(func() {
		stdinIsTerminal, confirmPrompt = origTTY, origConfirm
	})
}

// Non-interactive contexts must fail closed: no prompt, no build, an error
// that names the escape hatch (`construct rebuild`).
func TestConfirmConstructImageBuildFailsClosedNonInteractive(t *testing.T) {
	t.Setenv("CONSTRUCT_SKIP_IMAGE_BUILD", "")
	withAcquisitionSeams(t, false, false)

	err := ConfirmConstructImageBuild()
	if err == nil {
		t.Fatal("expected error in non-interactive context, got nil")
	}
	if !strings.Contains(err.Error(), "construct sys rebuild") {
		t.Errorf("error should point at `construct sys rebuild`, got: %v", err)
	}
	if !strings.Contains(err.Error(), "15") {
		t.Errorf("error should mention the build duration, got: %v", err)
	}
}

func TestConfirmConstructImageBuildConfirmed(t *testing.T) {
	t.Setenv("CONSTRUCT_SKIP_IMAGE_BUILD", "")
	withAcquisitionSeams(t, true, true)

	if err := ConfirmConstructImageBuild(); err != nil {
		t.Fatalf("expected confirmed build to pass, got: %v", err)
	}
}

func TestConfirmConstructImageBuildDeclined(t *testing.T) {
	t.Setenv("CONSTRUCT_SKIP_IMAGE_BUILD", "")
	withAcquisitionSeams(t, true, false)

	err := ConfirmConstructImageBuild()
	if err == nil {
		t.Fatal("expected error on declined build, got nil")
	}
	if !strings.Contains(err.Error(), "declined") {
		t.Errorf("error should record the decline, got: %v", err)
	}
}

// The skip env is declared intent to trust an externally provisioned image:
// it short-circuits both prompt and build regardless of interactivity.
func TestConfirmConstructImageBuildSkipEnvShortCircuits(t *testing.T) {
	t.Setenv("CONSTRUCT_SKIP_IMAGE_BUILD", "1")
	withAcquisitionSeams(t, false, false)

	if err := ConfirmConstructImageBuild(); err != nil {
		t.Fatalf("skip env must short-circuit to nil, got: %v", err)
	}
}

func TestImageCliBinary(t *testing.T) {
	cases := map[string]string{
		"docker":    "docker",
		"container": "docker", // macOS container runtime drives docker
		"podman":    "podman",
		"auto":      "docker",
	}
	for rt, want := range cases {
		if got := imageCliBinary(rt); got != want {
			t.Errorf("imageCliBinary(%q) = %q, want %q", rt, got, want)
		}
	}
}

func TestFirstLine(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "no output"},
		{"\n\n", "no output"},
		{"no such image\nsecond line", "no such image"},
		{"  leading spaces matter\nx", "leading spaces matter"},
		{"single line only", "single line only"},
	}
	for _, tc := range cases {
		if got := firstLine([]byte(tc.in)); got != tc.want {
			t.Errorf("firstLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The local probe must target the bare local name the templates reference.
func TestLocalConstructImageProbeTargetsLocalName(t *testing.T) {
	args := GetCheckImageCommand("docker")
	if len(args) < 4 || args[len(args)-1] != "construct-box:latest" {
		t.Fatalf("local image probe should reference construct-box:latest, got %v", args)
	}
}
