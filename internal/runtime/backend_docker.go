package runtime

import (
	"context"
	"fmt"

	"github.com/EstebanForge/construct-cli/internal/config"
)

// DockerBackend is the reference Backend implementation. It shells out to
// docker/podman via the package primitives; rt selects the binary
// ("docker", "container", "podman").
type DockerBackend struct {
	rt string
}

// NewDockerBackend returns a Backend over the given container runtime.
func NewDockerBackend(rt string) (*DockerBackend, error) {
	switch rt {
	case "docker", "container", "podman":
		return &DockerBackend{rt: rt}, nil
	default:
		return nil, fmt.Errorf("unsupported runtime: %s", rt)
	}
}

// Name returns the container runtime binary name this backend drives.
func (d *DockerBackend) Name() string { return d.rt }

// Available reports whether the container runtime is running.
func (d *DockerBackend) Available(_ context.Context) (bool, error) {
	return IsRuntimeRunning(d.rt), nil
}

// EnsureImage delegates to BuildImage, which reports build failure itself
// (existing behavior; it does not return a status). The msb backend must
// return a real error here instead.
// EnsureImage delegates to BuildImage for the construct image.
func (d *DockerBackend) EnsureImage(cfg *config.Config) error {
	BuildImage(cfg)
	return nil
}

// Exec runs a command in a live container. Exit-code fidelity: docker exec
// failures (including 126/127) surface through the error string from
// ExecInContainerWithEnv; code is 0 on success, 1 on error, matching the
// current callers' handling of the underlying function.
// Exec runs a command in a live container and returns combined output plus exit code.
func (d *DockerBackend) Exec(_ context.Context, opts ExecOptions) (string, int, error) {
	out, err := ExecInContainerWithEnv(d.rt, opts.Name, opts.Command, opts.Env, opts.User)
	if err != nil {
		return out, 1, err
	}
	return out, 0, nil
}

// ExecStream runs a command with streamed stdio and returns the exit code.
func (d *DockerBackend) ExecStream(_ context.Context, opts ExecOptions) (int, error) {
	return ExecNonInteractiveStream(d.rt, opts.Name, opts.Command, opts.Env, opts.Workdir, opts.User)
}

// State reports the lifecycle state of the named container.
func (d *DockerBackend) State(_ context.Context, name string) (ContainerState, error) {
	return GetContainerState(d.rt, name), nil
}

// WorkingDir inspects the container default working directory.
func (d *DockerBackend) WorkingDir(_ context.Context, name string) (string, error) {
	return GetContainerWorkingDir(d.rt, name)
}

// MountSource resolves the host-side source path of a mounted destination.
func (d *DockerBackend) MountSource(_ context.Context, name, destination string) (string, error) {
	return GetContainerMountSource(d.rt, name, destination)
}

// Label reads a label from the container.
func (d *DockerBackend) Label(_ context.Context, name, key string) (string, error) {
	return GetContainerLabel(d.rt, name, key)
}

// ListByPrefix lists container names matching a prefix.
func (d *DockerBackend) ListByPrefix(_ context.Context, prefix string) []string {
	return ListContainersByPrefix(d.rt, prefix)
}

// IsStale reports whether the container image differs from the current local image.
func (d *DockerBackend) IsStale(_ context.Context, name, imageName string) bool {
	return IsContainerStale(d.rt, name, imageName)
}

// Stop terminates a running container.
func (d *DockerBackend) Stop(_ context.Context, name string) error {
	return StopContainer(d.rt, name)
}

// Cleanup removes a non-running container.
func (d *DockerBackend) Cleanup(_ context.Context, name string) error {
	return CleanupExitedContainer(d.rt, name)
}

// CheckImageCommand returns the command verifying the local image exists.
func (d *DockerBackend) CheckImageCommand() []string {
	return GetCheckImageCommand(d.rt)
}

// Interface conformance.
var _ Backend = (*DockerBackend)(nil)
