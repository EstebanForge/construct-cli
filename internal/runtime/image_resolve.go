package runtime

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// Engine-uniform image acquisition: every backend resolves the construct
// image through the same three steps: local store first, then the published
// GHCR image, then a user-confirmed local build. The build is never started
// silently: it takes ~15 minutes on modern hardware, so it only runs after
// an explicit confirmation in an interactive terminal.

// imageCliBinary maps a resolved container runtime to the CLI binary that
// drives image operations (macOS "container" runtime drives docker).
func imageCliBinary(containerRuntime string) string {
	switch containerRuntime {
	case "podman":
		return "podman"
	default:
		return "docker"
	}
}

// EnsureConstructImage is the shared three-step acquisition flow used by
// both backends and the compose run path: local image, published image,
// confirmed local build. It resolves the container runtime exactly once.
func EnsureConstructImage(cfg *config.Config) error {
	rt := ResolveContainerRuntime(cfg)
	if localImageExists(rt) {
		return nil
	}
	if tryPullConstructImage(rt) {
		ui.InfoLn("✓ Published construct image ready")
		return nil
	}
	if err := ConfirmConstructImageBuild(); err != nil {
		return err
	}
	// BuildImage reports build failure itself and exits the process
	// (existing behavior).
	BuildImage(cfg)
	return nil
}

// LocalConstructImageExists reports whether the local construct image
// (construct-box:latest) exists in the host docker/podman store.
func LocalConstructImageExists(cfg *config.Config) bool {
	return localImageExists(ResolveContainerRuntime(cfg))
}

// localImageExists probes the resolved runtime's store for the bare local
// name the templates and staleness checks reference.
func localImageExists(rt string) bool {
	checkCmdArgs := GetCheckImageCommand(rt)
	checkCmd := exec.Command(checkCmdArgs[0], checkCmdArgs[1:]...)
	checkCmd.Dir = config.GetContainerDir()
	return checkCmd.Run() == nil
}

// tryPullConstructImage pulls the published image from GHCR into the local
// docker/podman store and aliases it to the local name the templates
// reference (construct-box:latest). Best-effort: false means the published
// image could not be fetched (missing upstream, offline, registry error).
func tryPullConstructImage(rt string) bool {
	bin := imageCliBinary(rt)
	ui.InfoF("→ Pulling published image (%s, ~7.7 GiB)...\n", PrepullImageRef)
	pull := exec.Command(bin, "pull", PrepullImageRef)
	if out, err := pull.CombinedOutput(); err != nil {
		ui.InfoF("→ Published image unavailable: %s\n", firstLine(out))
		return false
	}
	// The templates and staleness probes reference the bare local name;
	// alias the registry ref down to it.
	tag := exec.Command(bin, "tag", PrepullImageRef, "construct-box:latest")
	if out, err := tag.CombinedOutput(); err != nil {
		ui.InfoF("→ Failed to alias published image to construct-box:latest: %v: %s\n", err, firstLine(out))
		return false
	}
	return true
}

// Test seams so the confirmation flow is unit-testable without a terminal
// or a real build.
var (
	stdinIsTerminal = ui.StdinIsTerminal
	confirmPrompt   = ui.GumConfirm
)

// ConfirmConstructImageBuild gates the local image build behind an explicit
// user confirmation. Non-interactive contexts fail closed with instructions
// instead of silently starting a ~15-minute build. All output goes to
// stderr: this runs on agent launch paths whose stdout is a protocol stream.
func ConfirmConstructImageBuild() error {
	if shouldSkipImageBuild() {
		// Declared intent (CI, lab fixtures): trust the externally
		// provisioned image and skip both prompt and build.
		return nil
	}
	if !stdinIsTerminal() {
		return fmt.Errorf("construct-box image not found locally and the published image could not be pulled; run `construct sys rebuild` (or `docker pull %s`) from a terminal: refusing to start a ~15-minute build without a confirmed prompt", PrepullImageRef)
	}
	ui.InfoLn("⚠️  Construct image not found locally and the published image is unavailable.")
	if !confirmPrompt("Build the image locally now? (~15 min on modern hardware)") {
		return fmt.Errorf("image build declined; run `construct sys rebuild` later, or pull %s manually", PrepullImageRef)
	}
	return nil
}

// firstLine returns the first non-blank line of command output for short
// error context, whitespace-trimmed.
func firstLine(out []byte) string {
	for _, line := range strings.Split(string(out), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return "no output"
}
