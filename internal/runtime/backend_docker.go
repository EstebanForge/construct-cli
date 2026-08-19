// Package runtime: DockerBackend implements Backend over the existing
// Docker/Podman container primitives (Step 4, docs/VMs.md §7). Pure
// delegation: the package-level functions keep their signatures and
// behavior; this file adds the Backend facade only.
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

func (d *DockerBackend) Name() string { return d.rt }

func (d *DockerBackend) Available(ctx context.Context) (bool, error) {
	return IsRuntimeRunning(d.rt), nil
}

// EnsureImage delegates to BuildImage, which reports build failure itself
// (existing behavior; it does not return a status). The msb backend must
// return a real error here instead.
func (d *DockerBackend) EnsureImage(cfg *config.Config) error {
	BuildImage(cfg)
	return nil
}

// Exec runs a command in a live container. Exit-code fidelity: docker exec
// failures (including 126/127) surface through the error string from
// ExecInContainerWithEnv; code is 0 on success, 1 on error, matching the
// current callers' handling of the underlying function.
func (d *DockerBackend) Exec(ctx context.Context, opts ExecOptions) (string, int, error) {
	out, err := ExecInContainerWithEnv(d.rt, opts.Name, opts.Command, opts.Env, opts.User)
	if err != nil {
		return out, 1, err
	}
	return out, 0, nil
}

func (d *DockerBackend) ExecStream(ctx context.Context, opts ExecOptions) (int, error) {
	return ExecNonInteractiveStream(d.rt, opts.Name, opts.Command, opts.Env, opts.Workdir, opts.User)
}

func (d *DockerBackend) State(ctx context.Context, name string) (ContainerState, error) {
	return GetContainerState(d.rt, name), nil
}

func (d *DockerBackend) WorkingDir(ctx context.Context, name string) (string, error) {
	return GetContainerWorkingDir(d.rt, name)
}

func (d *DockerBackend) MountSource(ctx context.Context, name, destination string) (string, error) {
	return GetContainerMountSource(d.rt, name, destination)
}

func (d *DockerBackend) Label(ctx context.Context, name, key string) (string, error) {
	return GetContainerLabel(d.rt, name, key)
}

func (d *DockerBackend) ListByPrefix(ctx context.Context, prefix string) []string {
	return ListContainersByPrefix(d.rt, prefix)
}

func (d *DockerBackend) IsStale(ctx context.Context, name, imageName string) bool {
	return IsContainerStale(d.rt, name, imageName)
}

func (d *DockerBackend) Stop(ctx context.Context, name string) error {
	return StopContainer(d.rt, name)
}

func (d *DockerBackend) Cleanup(ctx context.Context, name string) error {
	return CleanupExitedContainer(d.rt, name)
}

func (d *DockerBackend) CheckImageCommand() []string {
	return GetCheckImageCommand(d.rt)
}

// Interface conformance.
var _ Backend = (*DockerBackend)(nil)
