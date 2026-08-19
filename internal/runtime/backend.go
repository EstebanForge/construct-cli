// Package runtime: Backend interface for isolation backends.
//
// The Backend interface covers the backend-agnostic primitive families from
// the Step 3 inventory (docs/VMs.md §4.1): exec, inspect, lifecycle,
// image/setup, mounts, env assembly, naming/labels. Host-probe
// (DetectRuntime, IsRuntimeRunning) and compose-assembly
// (BuildComposeCommand, GenerateDockerComposeOverride) families are
// Docker-specific and stay outside this interface.
//
// Docker is the reference implementation (backend_docker.go). The msb
// microVM backend (Step 6) must pass the same conformance suite
// (conformance_test.go).
package runtime

import (
	"context"

	"github.com/EstebanForge/construct-cli/internal/config"
)

// Backend launches and manages the construct isolation environment.
// Implementations must surface workload exit codes on every exec/run path
// (126/127 PATH fidelity included); callers rely on them (engine.go exit
// hint paths).
type Backend interface {
	// Name returns the backend identifier ("docker", "msb").
	Name() string

	// Available reports whether the backend is installed and running.
	Available(ctx context.Context) (bool, error)

	// EnsureImage guarantees the construct image exists locally
	// (build, pull, or load).
	EnsureImage(cfg *config.Config) error

	// Exec runs a command in a live environment and returns combined
	// output plus the workload exit code.
	Exec(ctx context.Context, opts ExecOptions) (string, int, error)

	// ExecStream runs a command with streamed stdio (non-interactive)
	// and returns the workload exit code.
	ExecStream(ctx context.Context, opts ExecOptions) (int, error)

	// State reports the lifecycle state of the named environment.
	State(ctx context.Context, name string) (ContainerState, error)

	// WorkingDir inspects the environment's default working directory.
	WorkingDir(ctx context.Context, name string) (string, error)

	// MountSource resolves the host-side source path for a mounted
	// destination inside the environment.
	MountSource(ctx context.Context, name, destination string) (string, error)

	// Label reads a label from the environment.
	Label(ctx context.Context, name, key string) (string, error)

	// ListByPrefix lists environment names matching a prefix.
	ListByPrefix(ctx context.Context, prefix string) []string

	// IsStale reports whether the environment's image differs from the
	// current local image (recreate needed).
	IsStale(ctx context.Context, name, imageName string) bool

	// Stop terminates a running environment.
	Stop(ctx context.Context, name string) error

	// Cleanup removes a non-running environment so it can be recreated.
	Cleanup(ctx context.Context, name string) error

	// CheckImageCommand returns the backend command that verifies the
	// image exists locally (used by doctor / staleness probes).
	CheckImageCommand() []string
}

// ExecOptions carries the backend-agnostic exec parameters.
type ExecOptions struct {
	Name    string   // environment name (CwdContainerName-derived)
	Command []string // argv
	Env     []string // ordered; callers may mutate in place (engine.go masking)
	Workdir string
	User    string // empty = backend default
}
