// Package doctor provides system health checks.
package doctor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"syscall"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/constants"
	runtimepkg "github.com/EstebanForge/construct-cli/internal/runtime"
	"github.com/EstebanForge/construct-cli/internal/sys"
	"github.com/EstebanForge/construct-cli/internal/templates"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// CheckStatus represents the result of a health check
type CheckStatus string

// CheckStatus values for health checks.
const (
	CheckStatusOK      CheckStatus = "OK"
	CheckStatusWarning CheckStatus = "WARNING"
	CheckStatusError   CheckStatus = "ERROR"
	CheckStatusSkipped CheckStatus = "SKIPPED"
)

// CheckResult represents the result of a single diagnostic check
type CheckResult struct {
	Name       string
	Status     CheckStatus
	Message    string
	Details    []string // Additional context lines
	Suggestion string   // How to fix if failed
}

// Report contains all health check results.
type Report struct {
	Checks      []CheckResult
	Summary     string
	HasErrors   bool
	HasWarnings bool
}

var execCombinedOutput = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

var runDockerComposeCommand = func(args ...string) ([]byte, error) {
	cmd := exec.Command("docker", args...)
	cmd.Env = doctorComposeEnv()
	cmd.Dir = config.GetContainerDir()
	return cmd.CombinedOutput()
}

var getContainerStateFn = runtimepkg.GetContainerState
var stopContainerFn = runtimepkg.StopContainer
var cleanupExitedContainerFn = runtimepkg.CleanupExitedContainer
var buildComposeCommandFn = runtimepkg.BuildComposeCommand
var getCheckImageCommandFn = runtimepkg.GetCheckImageCommand
var runtimeUsesUserNamespaceRemapFn = runtimepkg.UsesUserNamespaceRemap

var runSudoCombinedOutput = func(args ...string) ([]byte, error) {
	return exec.Command("sudo", args...).CombinedOutput()
}

var runSudoInteractive = func(args ...string) error {
	cmd := exec.Command("sudo", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

var runChmodRecursive = func(path string) error {
	return exec.Command("chmod", "-R", "u+rwX", path).Run()
}

var runChownRecursive = func(owner, path string) error {
	return exec.Command("chown", "-R", owner, path).Run()
}

var runPodmanUnshareChown = func(args ...string) ([]byte, error) {
	podmanArgs := append([]string{"unshare", "chown"}, args...)
	return exec.Command("podman", podmanArgs...).CombinedOutput()
}

var runPodmanUnshareChmod = func(args ...string) ([]byte, error) {
	podmanArgs := append([]string{"unshare", "chmod"}, args...)
	return exec.Command("podman", podmanArgs...).CombinedOutput()
}

var getOwnerUID = func(path string) (int, error) {
	info, err := os.Stat(path)
	if err != nil {
		return -1, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return -1, fmt.Errorf("unsupported file stat for %s", path)
	}
	return int(stat.Uid), nil
}

var currentUID = func() int {
	return os.Getuid()
}

var currentGID = func() int {
	return os.Getgid()
}

var stdinIsTTY = func() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// Run performs system health checks and prints a report.
func Run(args ...string) {
	fmt.Println()
	if ui.GumAvailable() {
		cmd := ui.GetGumCommand("style", "--border", "rounded", "--padding", "1 2", "--bold", "The Construct Doctor")
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render header: %v\n", err)
		}
	} else {
		fmt.Println("=== The Construct Doctor ===")
	}
	fmt.Println()

	checks := make([]CheckResult, 0, 15)
	fixRequested := false
	for _, arg := range args {
		if arg == "--fix" {
			fixRequested = true
		}
	}

	// 0. Version Check
	versionCheck := CheckResult{
		Name:    "Construct Version",
		Status:  CheckStatusOK,
		Message: constants.Version,
	}
	checks = append(checks, versionCheck)

	// 1. Host System Info
	hostCheck := CheckResult{Name: "Host System", Status: CheckStatusOK}
	hostCheck.Message = fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)

	// Get OS-specific details
	var details []string
	switch runtime.GOOS {
	case "darwin":
		if swVers, err := exec.Command("sw_vers", "-productVersion").Output(); err == nil {
			details = append(details, fmt.Sprintf("macOS %s", strings.TrimSpace(string(swVers))))
		}
		if kernel, err := exec.Command("uname", "-r").Output(); err == nil {
			details = append(details, fmt.Sprintf("Kernel: %s", strings.TrimSpace(string(kernel))))
		}
	case "linux":
		// Check if running under WSL
		if procVersion, err := os.ReadFile("/proc/version"); err == nil {
			if strings.Contains(strings.ToLower(string(procVersion)), "microsoft") ||
				strings.Contains(strings.ToLower(string(procVersion)), "wsl") {
				details = append(details, "WSL (Windows Subsystem for Linux)")
			}
		}
		// Try to get OS info from /etc/os-release
		if data, err := os.ReadFile("/etc/os-release"); err == nil {
			lines := strings.Split(string(data), "\n")
			for _, line := range lines {
				if strings.HasPrefix(line, "PRETTY_NAME=") {
					osName := strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"")
					details = append(details, osName)
					break
				}
			}
		}
		// Get kernel version
		if kernel, err := exec.Command("uname", "-r").Output(); err == nil {
			details = append(details, fmt.Sprintf("Kernel: %s", strings.TrimSpace(string(kernel))))
		}
	default:
		details = append(details, runtime.GOOS)
	}
	hostCheck.Details = details
	checks = append(checks, hostCheck)

	// Load config once (used across checks)
	cfg, _, err := config.Load()
	if err != nil {
		ui.LogWarning("Failed to load config: %v", err)
	}

	// Runtime is needed by multiple checks (env warnings, compose health/fixes, linux remap handling).
	engine := "auto"
	if cfg != nil {
		engine = cfg.Runtime.Engine
	}
	runtimeName := runtimepkg.DetectRuntime(engine)

	// 2. Environment Check
	envCheck := CheckResult{Name: "Environment"}
	hostUID := hostID("-u")
	hostGID := hostID("-g")
	overridePath := filepath.Join(config.GetContainerDir(), "docker-compose.override.yml")
	mapping, err := composeUserMapping(overridePath)
	if hostUID == "" || hostGID == "" {
		envCheck.Status = CheckStatusSkipped
		envCheck.Message = "Host UID/GID unavailable"
	} else {
		envCheck.Status = CheckStatusOK
		envCheck.Message = "Host UID/GID detected"
		envCheck.Details = append(envCheck.Details, fmt.Sprintf("Host UID:GID: %s:%s", hostUID, hostGID))
	}
	if err != nil {
		envCheck.Status = CheckStatusWarning
		envCheck.Message = "Failed to read compose user mapping"
		envCheck.Details = append(envCheck.Details, err.Error())
	} else if mapping != "" {
		envCheck.Details = append(envCheck.Details, fmt.Sprintf("Container user mapping: %s", mapping))
		switch runtimeName {
		case "docker":
			if cfg != nil && !cfg.Sandbox.NonRootStrict && !cfg.Sandbox.AllowCustomOverride {
				envCheck.Status = CheckStatusWarning
				envCheck.Message = "Manual container user mapping detected"
				envCheck.Suggestion = "Remove 'user:' from docker-compose.override.yml for Docker, set [sandbox].non_root_strict = true, or set [sandbox].allow_custom_compose_override = true"
			}
		case "podman":
			if runtime.GOOS == "linux" && runtimeUsesUserNamespaceRemapFn(runtimeName) && (cfg == nil || !cfg.Sandbox.AllowCustomOverride) {
				envCheck.Status = CheckStatusWarning
				envCheck.Message = "Stale podman user mapping detected for rootless mode"
				envCheck.Suggestion = "Run 'construct sys doctor --fix' to regenerate docker-compose.override.yml"
			}
		}
	} else {
		envCheck.Details = append(envCheck.Details, "Container user mapping: not set")
	}
	if cfg != nil && cfg.Sandbox.NonRootStrict {
		envCheck.Details = append(envCheck.Details, "non_root_strict: enabled")
		envCheck.Details = append(envCheck.Details, "Limitation: root bootstrap permission fixes are disabled; brew/npm setup may fail")
	} else {
		envCheck.Details = append(envCheck.Details, "non_root_strict: disabled")
	}
	if cfg != nil && cfg.Sandbox.AllowCustomOverride {
		envCheck.Details = append(envCheck.Details, "allow_custom_compose_override: enabled")
	} else {
		envCheck.Details = append(envCheck.Details, "allow_custom_compose_override: disabled")
	}
	if cfg != nil && cfg.Sandbox.ExecAsHostUser {
		envCheck.Details = append(envCheck.Details, "exec_as_host_user: enabled")
	} else {
		envCheck.Details = append(envCheck.Details, "exec_as_host_user: disabled")
	}
	checks = append(checks, envCheck)

	// 3. CT Symlink Check
	ctCheck := CheckResult{Name: "CT Symlink"}
	changed, msg, err := sys.FixCtSymlink()
	if err != nil {
		ctCheck.Status = CheckStatusWarning
		ctCheck.Message = "Failed to fix ct symlink"
		ctCheck.Details = append(ctCheck.Details, err.Error())
		ctCheck.Suggestion = "Run 'construct sys ct-fix' manually"
	} else {
		ctCheck.Status = CheckStatusOK
		ctCheck.Message = msg
		if changed {
			ctCheck.Details = append(ctCheck.Details, "Symlink updated")
		}
	}
	checks = append(checks, ctCheck)

	// 4. Runtime Check
	runtimeCheck := CheckResult{Name: "Container Runtime"}
	if runtimeName != "" {
		runtimeCheck.Status = CheckStatusOK
		// Add (OrbStack) suffix if OrbStack is running on macOS
		runtimeDisplay := runtimeName
		if runtimeName == "docker" && runtimepkg.IsOrbStackRunning() {
			runtimeDisplay = "docker (OrbStack)"
		}
		runtimeCheck.Message = fmt.Sprintf("Found %s", runtimeDisplay)
		// Check version/status
		if version := runtimeVersion(runtimeName); version != "" {
			runtimeCheck.Details = append(runtimeCheck.Details, fmt.Sprintf("Version: %s", version))
		}
		if runtimepkg.IsRuntimeRunning(runtimeName) {
			runtimeCheck.Details = append(runtimeCheck.Details, "Runtime is running")
		} else {
			runtimeCheck.Status = CheckStatusError
			runtimeCheck.Message = fmt.Sprintf("%s is installed but not running", runtimeName)
			runtimeCheck.Suggestion = fmt.Sprintf("Start %s manually", runtimeName)
		}
	} else {
		runtimeCheck.Status = CheckStatusError
		runtimeCheck.Message = "No compatible runtime found"
		runtimeCheck.Suggestion = "Install Docker Desktop, Podman, or OrbStack"
	}
	if runtimeName == "docker" {
		if cfg != nil && cfg.Sandbox.NonRootStrict {
			runtimeCheck.Details = append(runtimeCheck.Details, "Docker mode: strict non-root enabled")
			runtimeCheck.Details = append(runtimeCheck.Details, "Recommendation: use runtime.engine='podman' for strict rootless workflows")
		} else {
			runtimeCheck.Details = append(runtimeCheck.Details, "Docker mode: root bootstrap for setup, then drop to non-root via gosu")
		}
	}
	checks = append(checks, runtimeCheck)

	if fixRequested && runtimeName != "" {
		overrideFixCheck := CheckResult{Name: "Compose Override Fix"}
		fixed, details, skipped, err := fixComposeOverride(runtimeName, cfg)
		if err != nil {
			overrideFixCheck.Status = CheckStatusWarning
			overrideFixCheck.Message = "Failed to reconcile compose override"
			overrideFixCheck.Details = append(overrideFixCheck.Details, details...)
			overrideFixCheck.Details = append(overrideFixCheck.Details, err.Error())
			overrideFixCheck.Suggestion = "Run 'construct sys doctor --fix' again after checking ~/.config/construct-cli/container/docker-compose.override.yml"
		} else if skipped {
			overrideFixCheck.Status = CheckStatusSkipped
			overrideFixCheck.Message = "Skipped (custom compose override management enabled)"
			overrideFixCheck.Details = append(overrideFixCheck.Details, details...)
		} else if fixed {
			overrideFixCheck.Status = CheckStatusOK
			overrideFixCheck.Message = "Compose override reconciled"
			overrideFixCheck.Details = append(overrideFixCheck.Details, details...)
		} else {
			overrideFixCheck.Status = CheckStatusOK
			overrideFixCheck.Message = "Compose override already up to date"
		}
		checks = append(checks, overrideFixCheck)
	}

	// 4.5 Compose Network State Check (Docker only)
	composeNetworkCheck := CheckResult{Name: "Compose Network State"}
	if runtimeName != "docker" {
		composeNetworkCheck.Status = CheckStatusSkipped
		composeNetworkCheck.Message = "Not applicable for current runtime"
	} else {
		needsRecreate, details, unsupportedDryRun, err := detectComposeNetworkRecreationIssue()
		switch {
		case unsupportedDryRun:
			composeNetworkCheck.Status = CheckStatusSkipped
			composeNetworkCheck.Message = "Dry-run not supported by current docker compose"
		case err != nil:
			composeNetworkCheck.Status = CheckStatusSkipped
			composeNetworkCheck.Message = "Unable to validate compose network state"
			composeNetworkCheck.Details = append(composeNetworkCheck.Details, err.Error())
		case needsRecreate:
			composeNetworkCheck.Status = CheckStatusWarning
			composeNetworkCheck.Message = "Compose network requires recreation"
			composeNetworkCheck.Details = append(composeNetworkCheck.Details, details...)
			composeNetworkCheck.Suggestion = "Run 'construct sys doctor --fix' to recreate stale compose networks"
		default:
			composeNetworkCheck.Status = CheckStatusOK
			composeNetworkCheck.Message = "Compose network settings are compatible"
		}
	}
	checks = append(checks, composeNetworkCheck)

	if fixRequested && runtimeName == "docker" {
		networkFixCheck := CheckResult{Name: "Compose Network Fix"}
		fixed, details, unsupportedDryRun, err := fixComposeNetworkRecreationIssue()
		switch {
		case unsupportedDryRun:
			networkFixCheck.Status = CheckStatusSkipped
			networkFixCheck.Message = "Skipped (docker compose dry-run unsupported)"
		case err != nil:
			networkFixCheck.Status = CheckStatusWarning
			networkFixCheck.Message = "Failed to apply compose network fix"
			networkFixCheck.Details = append(networkFixCheck.Details, err.Error())
			networkFixCheck.Suggestion = "Run 'docker compose down --remove-orphans' in ~/.config/construct-cli/container"
		case fixed:
			networkFixCheck.Status = CheckStatusOK
			networkFixCheck.Message = "Recreated stale compose network configuration"
			networkFixCheck.Details = append(networkFixCheck.Details, details...)
		default:
			networkFixCheck.Status = CheckStatusOK
			networkFixCheck.Message = "No compose network fix needed"
		}
		checks = append(checks, networkFixCheck)
	}

	if fixRequested && runtime.GOOS == "linux" {
		ownershipFixCheck := CheckResult{Name: "Config Ownership Fix"}
		fixed, details, err := fixLinuxConfigOwnership(config.GetConfigDir(), runtimeName)
		if err != nil {
			ownershipFixCheck.Status = CheckStatusWarning
			ownershipFixCheck.Message = "Failed to fix config ownership/permissions"
			ownershipFixCheck.Details = append(ownershipFixCheck.Details, details...)
			ownershipFixCheck.Details = append(ownershipFixCheck.Details, err.Error())
			if runtimeName == "podman" && runtimeUsesUserNamespaceRemapFn(runtimeName) {
				ownershipFixCheck.Suggestion = "Run: podman unshare chown -R 0:0 ~/.config/construct-cli && podman unshare chmod -R u+rwX ~/.config/construct-cli || sudo chown -R $(id -u):$(id -g) ~/.config/construct-cli && chmod -R u+rwX ~/.config/construct-cli"
			} else {
				ownershipFixCheck.Suggestion = "Run: sudo chown -R $(id -u):$(id -g) ~/.config/construct-cli && chmod -R u+rwX ~/.config/construct-cli"
			}
		} else if fixed {
			ownershipFixCheck.Status = CheckStatusOK
			ownershipFixCheck.Message = "Config ownership/permissions fixed"
			ownershipFixCheck.Details = append(ownershipFixCheck.Details, details...)
		} else {
			ownershipFixCheck.Status = CheckStatusOK
			ownershipFixCheck.Message = "No config ownership fix needed"
		}
		checks = append(checks, ownershipFixCheck)

		imageFixCheck := CheckResult{Name: "Image Rebuild Fix"}
		rebuilt, rebuildDetails, err := rebuildImageForEntrypointFix(runtimeName, config.GetConfigDir())
		if err != nil {
			imageFixCheck.Status = CheckStatusWarning
			imageFixCheck.Message = "Failed to rebuild image for startup fix"
			imageFixCheck.Details = append(imageFixCheck.Details, rebuildDetails...)
			imageFixCheck.Details = append(imageFixCheck.Details, err.Error())
			imageFixCheck.Suggestion = "Run: construct sys rebuild"
		} else if rebuilt {
			imageFixCheck.Status = CheckStatusOK
			imageFixCheck.Message = "Image rebuilt for startup ownership fix"
			imageFixCheck.Details = append(imageFixCheck.Details, rebuildDetails...)
		} else {
			imageFixCheck.Status = CheckStatusOK
			imageFixCheck.Message = "Image rebuild not needed"
		}
		checks = append(checks, imageFixCheck)

		sessionCleanupCheck := CheckResult{Name: "Session Container Fix"}
		cleaned, cleanupDetails, err := cleanupAgentContainer(runtimeName)
		if err != nil {
			sessionCleanupCheck.Status = CheckStatusWarning
			sessionCleanupCheck.Message = "Failed to recycle session container"
			sessionCleanupCheck.Details = append(sessionCleanupCheck.Details, cleanupDetails...)
			sessionCleanupCheck.Details = append(sessionCleanupCheck.Details, err.Error())
			sessionCleanupCheck.Suggestion = "Run: construct sys daemon stop && docker rm -f construct-cli (or podman rm -f construct-cli)"
		} else if cleaned {
			sessionCleanupCheck.Status = CheckStatusOK
			sessionCleanupCheck.Message = "Session container recycled"
			sessionCleanupCheck.Details = append(sessionCleanupCheck.Details, cleanupDetails...)
		} else {
			sessionCleanupCheck.Status = CheckStatusOK
			sessionCleanupCheck.Message = "No session container cleanup needed"
		}
		checks = append(checks, sessionCleanupCheck)

		daemonRecreateCheck := CheckResult{Name: "Daemon Recreate Fix"}
		recreated, recreateDetails, err := recreateDaemonContainer(runtimeName, config.GetConfigDir())
		if err != nil {
			daemonRecreateCheck.Status = CheckStatusWarning
			daemonRecreateCheck.Message = "Failed to recreate daemon container"
			daemonRecreateCheck.Details = append(daemonRecreateCheck.Details, recreateDetails...)
			daemonRecreateCheck.Details = append(daemonRecreateCheck.Details, err.Error())
			daemonRecreateCheck.Suggestion = "Run: construct sys daemon stop && construct sys daemon start"
		} else if recreated {
			daemonRecreateCheck.Status = CheckStatusOK
			daemonRecreateCheck.Message = "Daemon container recreated"
			daemonRecreateCheck.Details = append(daemonRecreateCheck.Details, recreateDetails...)
		} else {
			daemonRecreateCheck.Status = CheckStatusOK
			daemonRecreateCheck.Message = "No daemon recreation needed"
		}
		checks = append(checks, daemonRecreateCheck)
	}

	// 5. Daemon Mode Check
	daemonCheck := CheckResult{Name: "Daemon Mode"}
	if cfg == nil || runtimeName == "" {
		daemonCheck.Status = CheckStatusSkipped
		daemonCheck.Message = "Unavailable (config/runtime missing)"
	} else {
		daemonCheck.Details = append(daemonCheck.Details, fmt.Sprintf("Auto-start: %t", cfg.Daemon.AutoStart))
		daemonCheck.Details = append(daemonCheck.Details, fmt.Sprintf("Multi-path mounts: %t", cfg.Daemon.MultiPathsEnabled))

		daemonState := runtimepkg.GetContainerState(runtimeName, "construct-cli-daemon")
		switch daemonState {
		case runtimepkg.ContainerStateRunning:
			daemonCheck.Status = CheckStatusOK
			daemonCheck.Message = "Daemon is running"
		case runtimepkg.ContainerStateExited:
			daemonCheck.Status = CheckStatusWarning
			daemonCheck.Message = "Daemon container exists but is stopped"
			daemonCheck.Suggestion = "Run 'construct sys daemon start' to enable fast daemon execution"
		default:
			daemonCheck.Status = CheckStatusSkipped
			daemonCheck.Message = "Daemon is not running"
		}

		if cfg.Daemon.MultiPathsEnabled {
			mounts := runtimepkg.ResolveDaemonMounts(cfg)
			if len(mounts.Paths) == 0 {
				daemonCheck.Details = append(daemonCheck.Details, "Configured mount paths: none")
			} else {
				for _, path := range mounts.Paths {
					daemonCheck.Details = append(daemonCheck.Details, fmt.Sprintf("Configured mount path: %s", path))
				}
			}
			for _, warning := range mounts.Warnings {
				daemonCheck.Details = append(daemonCheck.Details, fmt.Sprintf("Mount warning: %s", warning))
			}
		}
	}
	checks = append(checks, daemonCheck)

	// 6. Config Check
	configCheck := CheckResult{Name: "Configuration"}
	configPath := filepath.Join(config.GetConfigDir(), "config.toml")
	if _, err := os.Stat(configPath); err == nil {
		configCheck.Status = CheckStatusOK
		configCheck.Message = "Valid config.toml found"
	} else {
		configCheck.Status = CheckStatusError
		configCheck.Message = "Config file missing"
		configCheck.Suggestion = "Run 'construct sys init'"
	}
	checks = append(checks, configCheck)

	if fixRequested {
		fixCheck := CheckResult{Name: "Config Fix"}
		changed, added, err := config.FixMissingDefaults(configPath)
		if err != nil {
			fixCheck.Status = CheckStatusWarning
			fixCheck.Message = "Failed to apply config defaults"
			fixCheck.Details = append(fixCheck.Details, err.Error())
		} else if !changed {
			fixCheck.Status = CheckStatusOK
			fixCheck.Message = "No defaults needed"
		} else {
			fixCheck.Status = CheckStatusOK
			fixCheck.Message = fmt.Sprintf("Added %d default values", len(added))
			fixCheck.Details = append(fixCheck.Details, added...)
		}
		checks = append(checks, fixCheck)
	}

	missingCheck := CheckResult{Name: "Config Defaults"}
	missingKeys, err := config.FindMissingKeys(configPath)
	if err != nil {
		missingCheck.Status = CheckStatusWarning
		missingCheck.Message = "Unable to check config defaults"
		missingCheck.Details = append(missingCheck.Details, err.Error())
	} else if len(missingKeys) == 0 {
		missingCheck.Status = CheckStatusOK
		missingCheck.Message = "All defaults present"
	} else {
		missingCheck.Status = CheckStatusWarning
		missingCheck.Message = fmt.Sprintf("Missing %d default values", len(missingKeys))
		missingCheck.Details = append(missingCheck.Details, missingKeys...)
		missingCheck.Suggestion = "Run 'construct sys doctor --fix'"
	}
	checks = append(checks, missingCheck)

	// 6.5 Config Permissions Check (Linux/WSL)
	if runtime.GOOS == "linux" {
		permCheck := CheckResult{Name: "Config Permissions"}
		state := inspectLinuxConfigPermissionState(config.GetConfigDir())
		permCheck.Details = append(permCheck.Details, state.Details...)
		if runtimeUsesUserNamespaceRemapFn(runtimeName) {
			permCheck.Details = append(permCheck.Details, fmt.Sprintf("Runtime mode: %s userns remap detected", runtimeName))
		}

		if len(state.Missing) > 0 {
			permCheck.Status = CheckStatusWarning
			permCheck.Message = "Config directories missing"
			permCheck.Suggestion = "Run 'construct sys init' or 'construct sys config --migrate'"
		} else if state.HasProblems() {
			permCheck.Status = CheckStatusWarning
			permCheck.Message = "Config ownership/permissions mismatch"
			if runtimeName == "podman" && runtimeUsesUserNamespaceRemapFn(runtimeName) {
				permCheck.Suggestion = "Run 'construct sys doctor --fix' or manually: podman unshare chown -R 0:0 ~/.config/construct-cli (fallback: sudo chown -R $(id -u):$(id -g) ~/.config/construct-cli)"
			} else {
				permCheck.Suggestion = "Run 'construct sys doctor --fix' or manually: sudo chown -R $(id -u):$(id -g) ~/.config/construct-cli"
			}
		} else {
			permCheck.Status = CheckStatusOK
			permCheck.Message = "Config directories writable and owned by current user"
		}
		checks = append(checks, permCheck)
	}

	// 7. Setup Log Check
	setupCheck := CheckResult{Name: "Setup Log"}
	logDir := filepath.Join(config.GetConfigDir(), "logs")
	logPath, err := latestLogFile(logDir, "setup_install_*.log")
	if err != nil || logPath == "" {
		setupCheck.Status = CheckStatusSkipped
		setupCheck.Message = "No setup log found"
	} else {
		setupCheck.Details = append(setupCheck.Details, fmt.Sprintf("Last log: %s", logPath))
		logData, err := os.ReadFile(logPath)
		if err != nil {
			setupCheck.Status = CheckStatusWarning
			setupCheck.Message = "Failed to read setup log"
			setupCheck.Suggestion = "Re-run 'construct sys shell' to regenerate logs"
		} else {
			logText := string(logData)
			if strings.Contains(logText, "Failed to install") || strings.Contains(logText, "Package installation encountered errors") {
				setupCheck.Status = CheckStatusWarning
				setupCheck.Message = "Setup completed with installation errors"
				setupCheck.Suggestion = "Review the setup log for failed packages"
			} else {
				setupCheck.Status = CheckStatusOK
				setupCheck.Message = "Setup completed without logged errors"
			}
		}
	}
	checks = append(checks, setupCheck)

	// 8. Update Log Check
	updateCheck := CheckResult{Name: "Update Log"}
	updateLogPath, err := latestLogFile(logDir, "update_*.log")
	if err != nil || updateLogPath == "" {
		updateCheck.Status = CheckStatusSkipped
		updateCheck.Message = "No update log found"
	} else {
		updateCheck.Status = CheckStatusOK
		updateCheck.Message = "Latest update log found"
		updateCheck.Details = append(updateCheck.Details, fmt.Sprintf("Last log: %s", updateLogPath))
	}
	checks = append(checks, updateCheck)

	// 9. Templates Check
	templatesCheck := CheckResult{Name: "Templates Sync"}
	templatesDir := config.GetContainerDir()
	if entries, err := os.ReadDir(templatesDir); err == nil && len(entries) > 0 {
		templatesCheck.Status = CheckStatusOK
		templatesCheck.Message = "Templates directory populated"
		templatesCheck.Details = append(templatesCheck.Details, fmt.Sprintf("Path: %s", templatesDir))
	} else {
		templatesCheck.Status = CheckStatusWarning
		templatesCheck.Message = "Templates directory missing or empty"
		templatesCheck.Suggestion = "Run 'construct sys config --migrate' to refresh templates"
	}
	checks = append(checks, templatesCheck)

	// 10. Packages Config Check
	packagesCheck := CheckResult{Name: "Packages Config"}
	if _, err := config.LoadPackages(); err != nil {
		packagesCheck.Status = CheckStatusError
		packagesCheck.Message = "packages.toml invalid or missing"
		packagesCheck.Details = append(packagesCheck.Details, err.Error())
		packagesCheck.Suggestion = "Run 'construct sys packages' to recreate a valid file"
	} else {
		packagesCheck.Status = CheckStatusOK
		packagesCheck.Message = "packages.toml loaded successfully"
	}
	checks = append(checks, packagesCheck)

	// 11. Entrypoint State Check
	entrypointCheck := CheckResult{Name: "Entrypoint State"}
	homeLocal := filepath.Join(config.GetConfigDir(), "home", ".local")
	hashPath := filepath.Join(homeLocal, ".entrypoint_hash")
	forcePath := filepath.Join(homeLocal, ".force_entrypoint")
	if _, err := os.Stat(forcePath); err == nil {
		entrypointCheck.Status = CheckStatusWarning
		entrypointCheck.Message = "Entrypoint setup forced on next run"
		entrypointCheck.Suggestion = "Start a shell to apply pending setup"
	} else if _, err := os.Stat(hashPath); err == nil {
		entrypointCheck.Status = CheckStatusOK
		entrypointCheck.Message = "Entrypoint setup hash present"
	} else {
		entrypointCheck.Status = CheckStatusWarning
		entrypointCheck.Message = "Entrypoint setup hash missing"
		entrypointCheck.Suggestion = "Start a shell to run setup"
	}
	checks = append(checks, entrypointCheck)

	// 12. Image Check
	imageCheck := CheckResult{Name: "Construct Image"}
	checkCmdArgs := runtimepkg.GetCheckImageCommand(runtimeName)
	checkCmd := exec.Command(checkCmdArgs[0], checkCmdArgs[1:]...)
	if err := checkCmd.Run(); err == nil {
		imageCheck.Status = CheckStatusOK
		imageCheck.Message = "Image exists (construct-box:latest)"
	} else {
		imageCheck.Status = CheckStatusWarning
		imageCheck.Message = "Image missing"
		imageCheck.Suggestion = "Run 'construct sys init' or run any agent to build"
	}
	checks = append(checks, imageCheck)

	// 13. SSH Agent Check
	sshCheck := CheckResult{Name: "SSH Agent"}
	if cfg != nil && !cfg.Sandbox.ForwardSSHAgent {
		sshCheck.Status = CheckStatusSkipped
		sshCheck.Message = "SSH Agent forwarding disabled"
		sshCheck.Suggestion = "Enable 'forward_ssh_agent' in config.toml to use SSH keys"
	} else {
		sshSock := os.Getenv("SSH_AUTH_SOCK")
		if sshSock != "" {
			sshCheck.Status = CheckStatusOK
			sshCheck.Message = "SSH Agent detected"
			sshCheck.Details = []string{fmt.Sprintf("Socket: %s", sshSock)}
		} else {
			sshCheck.Status = CheckStatusWarning
			sshCheck.Message = "SSH Agent not found"
			sshCheck.Suggestion = "Start ssh-agent and run 'ssh-add' to use SSH keys securely in the container"
		}
	}
	checks = append(checks, sshCheck)

	// 14. SSH Keys Check (Imported)
	keysCheck := CheckResult{Name: "Construct SSH Keys"}
	sshDir := filepath.Join(config.GetConfigDir(), "home", ".ssh")
	nonKeyFiles := map[string]bool{
		"known_hosts":     true,
		"known_hosts.old": true,
		"config":          true,
		"config.backup":   true,
		"authorized_keys": true,
		"agent.sock":      true,
	}
	if entries, err := os.ReadDir(sshDir); err == nil && len(entries) > 0 {
		var keyNames []string
		for _, entry := range entries {
			name := entry.Name()
			if !entry.IsDir() && !strings.HasSuffix(name, ".pub") && !nonKeyFiles[name] {
				keyNames = append(keyNames, name)
			}
		}
		if len(keyNames) > 0 {
			keysCheck.Status = CheckStatusOK
			keysCheck.Message = fmt.Sprintf("Found %d local keys", len(keyNames))
			keysCheck.Details = keyNames
		} else {
			keysCheck.Status = CheckStatusSkipped
			keysCheck.Message = "No local keys found"
		}
	} else {
		keysCheck.Status = CheckStatusSkipped
		keysCheck.Message = "No local keys found"
	}
	checks = append(checks, keysCheck)

	// 15. Clipboard Bridge Check
	clipboardCheck := CheckResult{Name: "Clipboard Bridge"}
	clipboardHost := ""
	networkMode := ""
	if cfg != nil {
		clipboardHost = cfg.Sandbox.ClipboardHost
		networkMode = cfg.Network.Mode
	}
	if clipboardHost == "" {
		clipboardCheck.Status = CheckStatusWarning
		clipboardCheck.Message = "clipboard_host not set"
		clipboardCheck.Suggestion = "Set [sandbox].clipboard_host in config.toml (default: host.docker.internal)"
	} else {
		clipboardCheck.Status = CheckStatusOK
		clipboardCheck.Message = "Clipboard host configured"
		clipboardCheck.Details = append(clipboardCheck.Details, fmt.Sprintf("Host: %s", clipboardHost))
		if networkMode != "" {
			clipboardCheck.Details = append(clipboardCheck.Details, fmt.Sprintf("Network mode: %s", networkMode))
		}
	}

	// Host clipboard tools availability
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("pbpaste"); err != nil {
			clipboardCheck.Status = CheckStatusWarning
			clipboardCheck.Message = "pbpaste not found on host"
			clipboardCheck.Suggestion = "Ensure macOS clipboard utilities are available"
		}
		if _, err := exec.LookPath("osascript"); err != nil {
			clipboardCheck.Status = CheckStatusWarning
			clipboardCheck.Message = "osascript not found on host"
			clipboardCheck.Suggestion = "Ensure osascript is available to read images"
		}
	case "linux":
		if _, err := exec.LookPath("wl-paste"); err != nil {
			if _, err := exec.LookPath("xclip"); err != nil {
				clipboardCheck.Status = CheckStatusWarning
				clipboardCheck.Message = "wl-paste/xclip not found on host"
				clipboardCheck.Suggestion = "Install wl-clipboard or xclip for clipboard access"
			}
		}
	}
	checks = append(checks, clipboardCheck)

	// Print Report
	for _, check := range checks {
		printCheckResult(check)
	}

	fmt.Println()
}

func runtimeVersion(runtimeName string) string {
	var cmd *exec.Cmd

	switch runtimeName {
	case "docker":
		cmd = exec.Command("docker", "version", "--format", "{{.Server.Version}}")
	case "podman":
		cmd = exec.Command("podman", "--version")
	case "container":
		cmd = exec.Command("container", "--version")
	default:
		return ""
	}

	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(output))
}

func latestLogFile(logDir, pattern string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(logDir, pattern))
	if err != nil || len(matches) == 0 {
		return "", err
	}

	sort.Slice(matches, func(i, j int) bool {
		infoA, errA := os.Stat(matches[i])
		infoB, errB := os.Stat(matches[j])
		if errA != nil || errB != nil {
			return matches[i] > matches[j]
		}
		return infoA.ModTime().After(infoB.ModTime())
	})

	return matches[0], nil
}

func hostID(flag string) string {
	output, err := exec.Command("id", flag).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func composeUserMapping(overridePath string) (string, error) {
	data, err := os.ReadFile(overridePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to read %s: %w", overridePath, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "user:") {
			value := strings.TrimSpace(strings.TrimPrefix(trimmed, "user:"))
			value = strings.Trim(value, "\"'")
			return value, nil
		}
	}
	return "", nil
}

func fixComposeOverride(runtimeName string, cfg *config.Config) (bool, []string, bool, error) {
	if runtimeName == "" {
		return false, nil, false, nil
	}
	if cfg != nil && cfg.Sandbox.AllowCustomOverride {
		return false, []string{"sandbox.allow_custom_compose_override=true"}, true, nil
	}

	configPath := config.GetConfigDir()
	containerDir := config.GetContainerDir()
	overridePath := filepath.Join(containerDir, "docker-compose.override.yml")

	var before []byte
	if data, err := os.ReadFile(overridePath); err == nil {
		before = data
	} else if !os.IsNotExist(err) {
		return false, nil, false, fmt.Errorf("failed to read current override: %w", err)
	}
	networkMode := "permissive"
	if cfg != nil {
		networkMode = cfg.Network.Mode
	}

	if err := runtimepkg.GenerateDockerComposeOverride(configPath, runtimepkg.GetProjectMountPath(), networkMode, runtimeName); err != nil {
		return false, nil, false, err
	}

	after, err := os.ReadFile(overridePath)
	if err != nil {
		return false, nil, false, fmt.Errorf("failed to read regenerated override: %w", err)
	}

	changed := !bytes.Equal(before, after)
	details := []string{fmt.Sprintf("Runtime: %s", runtimeName)}
	if changed {
		details = append(details, "Regenerated docker-compose.override.yml from current templates/runtime settings")
	}

	mapping, err := composeUserMapping(overridePath)
	if err != nil {
		return changed, details, false, err
	}

	if runtimeName == "docker" && mapping != "" && (cfg == nil || !cfg.Sandbox.NonRootStrict) {
		return changed, details, false, fmt.Errorf("override still contains docker user mapping while non_root_strict is disabled")
	}
	if runtimeName == "podman" && runtime.GOOS == "linux" && runtimeUsesUserNamespaceRemapFn(runtimeName) && mapping != "" {
		return changed, details, false, fmt.Errorf("override still contains podman user mapping in rootless userns mode")
	}

	return changed, details, false, nil
}

func detectComposeNetworkRecreationIssue() (bool, []string, bool, error) {
	output, unsupportedDryRun, err := runComposeDryRun()
	if unsupportedDryRun {
		return false, nil, true, nil
	}
	lines := parseComposeNetworkRecreationLines(output)
	if len(lines) > 0 {
		return true, lines, false, nil
	}
	if err != nil {
		return false, nil, false, fmt.Errorf("docker compose dry-run failed: %w", err)
	}
	return false, nil, false, nil
}

func fixComposeNetworkRecreationIssue() (bool, []string, bool, error) {
	output, unsupportedDryRun, err := runComposeDryRun()
	if unsupportedDryRun {
		return false, nil, true, nil
	}

	networkLines := parseComposeNetworkRecreationLines(output)
	if len(networkLines) == 0 {
		if err != nil {
			return false, nil, false, fmt.Errorf("docker compose dry-run failed: %w", err)
		}
		return false, nil, false, nil
	}

	composeArgs := composeBaseArgs()
	downArgs := append(append([]string{}, composeArgs...), "down", "--remove-orphans")
	downOutput, downErr := runDockerComposeCommand(downArgs...)
	if downErr != nil {
		return false, networkLines, false, fmt.Errorf("docker compose down failed: %w (%s)", downErr, strings.TrimSpace(string(downOutput)))
	}

	networks := extractComposeNetworkNames(output)
	var removed []string
	for _, network := range networks {
		if network == "" {
			continue
		}
		rmOutput, rmErr := execCombinedOutput("docker", "network", "rm", network)
		if rmErr != nil {
			rmText := strings.ToLower(string(rmOutput))
			if strings.Contains(rmText, "not found") || strings.Contains(rmText, "no such network") {
				continue
			}
			return false, networkLines, false, fmt.Errorf("docker network rm %s failed: %w (%s)", network, rmErr, strings.TrimSpace(string(rmOutput)))
		}
		removed = append(removed, network)
	}

	details := append([]string{}, networkLines...)
	if len(removed) > 0 {
		details = append(details, fmt.Sprintf("Removed networks: %s", strings.Join(removed, ", ")))
	}
	return true, details, false, nil
}

func runComposeDryRun() (string, bool, error) {
	composeArgs := composeBaseArgs()
	args := append(append([]string{}, composeArgs...), "up", "--dry-run", "--no-build", "construct-box")
	out, err := runDockerComposeCommand(args...)
	text := string(out)
	if strings.Contains(text, "unknown flag: --dry-run") {
		return text, true, nil
	}
	return text, false, err
}

func composeBaseArgs() []string {
	containerDir := config.GetContainerDir()
	composePath := filepath.Join(containerDir, "docker-compose.yml")
	overridePath := filepath.Join(containerDir, "docker-compose.override.yml")

	args := []string{"compose", "-f", composePath}
	if _, err := os.Stat(overridePath); err == nil {
		args = append(args, "-f", overridePath)
	}
	return args
}

func doctorComposeEnv() []string {
	env := runtimepkg.AppendProjectPathEnv(os.Environ())
	env = runtimepkg.AppendHostIdentityEnv(env)

	cwd, err := os.Getwd()
	if err == nil && cwd != "" {
		env = setEnvVar(env, "PWD", cwd)
	}

	return env
}

func setEnvVar(env []string, key, value string) []string {
	prefix := key + "="
	for i, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func parseComposeNetworkRecreationLines(output string) []string {
	var lines []string
	seen := map[string]bool{}

	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "needs to be recreated - option \"com.docker.network.enable_") {
			if !seen[trimmed] {
				seen[trimmed] = true
				lines = append(lines, trimmed)
			}
		}
	}

	return lines
}

func extractComposeNetworkNames(output string) []string {
	re := regexp.MustCompile(`Network "([^"]+)" needs to be recreated`)
	matches := re.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := map[string]bool{}
	var names []string
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		name := strings.TrimSpace(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

func printCheckResult(check CheckResult) {
	statusIcon := "✓"
	color := ui.ColorGreen

	switch check.Status {
	case CheckStatusWarning:
		statusIcon = "!"
		color = ui.ColorYellow
	case CheckStatusError:
		statusIcon = "✗"
		color = ui.ColorRed
	case CheckStatusSkipped:
		statusIcon = "-"
		color = ui.ColorGrey
	}

	// Print main check result with color
	fmt.Printf("%s%s %s: %s%s\n", color, statusIcon, check.Name, check.Message, ui.ColorReset)

	// Print details in grey
	for _, detail := range check.Details {
		fmt.Printf("%s  • %s%s\n", ui.ColorGrey, detail, ui.ColorReset)
	}

	// Print suggestion in yellow
	if check.Suggestion != "" {
		fmt.Printf("%s  → Suggestion: %s%s\n", ui.ColorYellow, check.Suggestion, ui.ColorReset)
	}
}

func isWritableDir(path string) (bool, error) {
	f, err := os.CreateTemp(path, ".construct-doctor-*")
	if err != nil {
		return false, err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		if removeErr := os.Remove(name); removeErr != nil {
			return false, fmt.Errorf("close temp file: %w (cleanup failed: %v)", err, removeErr)
		}
		return false, err
	}
	if err := os.Remove(name); err != nil {
		return false, err
	}
	return true, nil
}

type linuxConfigPermissionState struct {
	Missing     []string
	WrongOwner  []string
	NotWritable []string
	Details     []string
}

func (s linuxConfigPermissionState) HasProblems() bool {
	return len(s.WrongOwner) > 0 || len(s.NotWritable) > 0
}

func linuxConfigPaths(configDir string) []string {
	return []string{
		filepath.Join(configDir, "home"),
		filepath.Join(configDir, "container"),
	}
}

func inspectLinuxConfigPermissionState(configDir string) linuxConfigPermissionState {
	state := linuxConfigPermissionState{}
	uid := currentUID()

	for _, path := range linuxConfigPaths(configDir) {
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				state.Missing = append(state.Missing, path)
				state.Details = append(state.Details, fmt.Sprintf("Missing: %s", path))
				continue
			}
			state.NotWritable = append(state.NotWritable, path)
			state.Details = append(state.Details, fmt.Sprintf("Error: %s (%v)", path, err))
			continue
		}

		ownerUID, err := getOwnerUID(path)
		if err != nil {
			state.Details = append(state.Details, fmt.Sprintf("Owner check failed: %s (%v)", path, err))
		} else if ownerUID != uid {
			state.WrongOwner = append(state.WrongOwner, path)
			state.Details = append(state.Details, fmt.Sprintf("Wrong owner: %s (owner uid=%d, current uid=%d)", path, ownerUID, uid))
		}

		if ok, err := isWritableDir(path); !ok {
			state.NotWritable = append(state.NotWritable, path)
			state.Details = append(state.Details, fmt.Sprintf("Not writable: %s (%v)", path, err))
		}
	}

	return state
}

func fixLinuxConfigOwnership(configDir, runtimeName string) (bool, []string, error) {
	if runtime.GOOS != "linux" {
		return false, nil, nil
	}

	for _, path := range linuxConfigPaths(configDir) {
		if err := os.MkdirAll(path, 0755); err != nil {
			return false, nil, fmt.Errorf("failed to create %s: %w", path, err)
		}
	}

	state := inspectLinuxConfigPermissionState(configDir)
	if !state.HasProblems() {
		return false, nil, nil
	}

	uid := currentUID()
	gid := currentGID()
	owner := fmt.Sprintf("%d:%d", uid, gid)
	rootlessOwner := "0:0"
	details := append([]string{}, state.Details...)
	useUsernsRemap := runtimeUsesUserNamespaceRemapFn(runtimeName)
	usePodmanUnshareFix := runtimeName == "podman" && useUsernsRemap

	if len(state.WrongOwner) > 0 {
		ownerFixed := false

		if usePodmanUnshareFix {
			if out, err := runPodmanUnshareChown("-R", rootlessOwner, configDir); err == nil {
				details = append(details, fmt.Sprintf("Applied rootless ownership fix: podman unshare chown -R %s %s", rootlessOwner, configDir))
				afterRootless := inspectLinuxConfigPermissionState(configDir)
				if len(afterRootless.WrongOwner) == 0 {
					ownerFixed = true
				} else {
					details = append(details, "Rootless ownership fix did not restore host ownership; falling back to sudo chown")
				}
			} else {
				msg := strings.TrimSpace(string(out))
				if msg == "" {
					details = append(details, fmt.Sprintf("Rootless ownership fix failed: %v", err))
				} else {
					details = append(details, fmt.Sprintf("Rootless ownership fix failed: %v (%s)", err, msg))
				}
			}
		} else if useUsernsRemap {
			// Docker/container rootless/userns-remap: first try non-elevated host chown.
			if err := runChownRecursive(owner, configDir); err == nil {
				details = append(details, fmt.Sprintf("Applied ownership fix: chown -R %s %s", owner, configDir))
				ownerFixed = true
			}
		}

		if !ownerFixed {
			if out, err := runSudoCombinedOutput("-n", "chown", "-R", owner, configDir); err != nil {
				if !stdinIsTTY() {
					msg := strings.TrimSpace(string(out))
					if msg == "" {
						return false, details, fmt.Errorf("sudo non-interactive chown failed: %w", err)
					}
					return false, details, fmt.Errorf("sudo non-interactive chown failed: %w (%s)", err, msg)
				}
				if err := runSudoInteractive("chown", "-R", owner, configDir); err != nil {
					return false, details, fmt.Errorf("sudo chown failed: %w", err)
				}
			}
			details = append(details, fmt.Sprintf("Applied ownership fix: sudo chown -R %s %s", owner, configDir))
		}
	}

	if err := runChmodRecursive(configDir); err != nil {
		chmodFixed := false

		if usePodmanUnshareFix {
			if out, rootlessErr := runPodmanUnshareChmod("-R", "u+rwX", configDir); rootlessErr == nil {
				details = append(details, fmt.Sprintf("Ensured user write access (rootless): podman unshare chmod -R u+rwX %s", configDir))
				chmodFixed = true
			} else {
				msg := strings.TrimSpace(string(out))
				if msg == "" {
					details = append(details, fmt.Sprintf("Rootless chmod fix failed: %v", rootlessErr))
				} else {
					details = append(details, fmt.Sprintf("Rootless chmod fix failed: %v (%s)", rootlessErr, msg))
				}
			}
		}

		if !chmodFixed {
			if out, sudoErr := runSudoCombinedOutput("-n", "chmod", "-R", "u+rwX", configDir); sudoErr != nil {
				if !stdinIsTTY() {
					msg := strings.TrimSpace(string(out))
					if msg == "" {
						return false, details, fmt.Errorf("chmod failed: %w", err)
					}
					return false, details, fmt.Errorf("chmod failed: %w (sudo attempt failed: %s)", err, msg)
				}
				if sudoErr := runSudoInteractive("chmod", "-R", "u+rwX", configDir); sudoErr != nil {
					return false, details, fmt.Errorf("chmod failed: %w (sudo fallback failed: %v)", err, sudoErr)
				}
			}
			details = append(details, fmt.Sprintf("Ensured user write access: chmod -R u+rwX %s", configDir))
		}
	} else {
		details = append(details, fmt.Sprintf("Ensured user write access: chmod -R u+rwX %s", configDir))
	}

	after := inspectLinuxConfigPermissionState(configDir)
	if after.HasProblems() {
		details = append(details, after.Details...)
		return false, details, fmt.Errorf("config directory still has ownership/permission issues")
	}

	return true, details, nil
}

func cleanupAgentContainer(runtimeName string) (bool, []string, error) {
	if runtimeName == "" {
		return false, nil, nil
	}

	containerName := "construct-cli"
	state := getContainerStateFn(runtimeName, containerName)
	if state == runtimepkg.ContainerStateMissing {
		return false, nil, nil
	}

	details := []string{fmt.Sprintf("Previous session container state: %s", state)}
	if state == runtimepkg.ContainerStateRunning {
		if err := stopContainerFn(runtimeName, containerName); err != nil {
			return false, details, fmt.Errorf("failed to stop session container: %w", err)
		}
		details = append(details, "Stopped running session container")
	}
	if err := cleanupExitedContainerFn(runtimeName, containerName); err != nil {
		return false, details, fmt.Errorf("failed to remove session container: %w", err)
	}
	details = append(details, "Removed session container")
	return true, details, nil
}

func recreateDaemonContainer(runtimeName, configPath string) (bool, []string, error) {
	if runtimeName == "" {
		return false, nil, nil
	}

	daemonName := "construct-cli-daemon"
	state := getContainerStateFn(runtimeName, daemonName)
	if state == runtimepkg.ContainerStateMissing {
		return false, nil, nil
	}

	details := []string{fmt.Sprintf("Previous daemon state: %s", state)}
	if state == runtimepkg.ContainerStateRunning {
		if err := stopContainerFn(runtimeName, daemonName); err != nil {
			return false, details, fmt.Errorf("failed to stop daemon: %w", err)
		}
		details = append(details, "Stopped running daemon")
	}

	if err := cleanupExitedContainerFn(runtimeName, daemonName); err != nil {
		return false, details, fmt.Errorf("failed to remove daemon container: %w", err)
	}
	details = append(details, "Removed daemon container")

	cmd, err := buildComposeCommandFn(runtimeName, configPath, "run", []string{"-d", "--name", daemonName, "construct-box"})
	if err != nil {
		return false, details, fmt.Errorf("failed to build daemon recreate command: %w", err)
	}
	cmd.Dir = configPath
	cmd.Env = doctorComposeEnv()

	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			return false, details, fmt.Errorf("failed to start daemon: %w", err)
		}
		return false, details, fmt.Errorf("failed to start daemon: %w (%s)", err, msg)
	}

	details = append(details, "Started fresh daemon container")
	return true, details, nil
}

func rebuildImageForEntrypointFix(runtimeName, configPath string) (bool, []string, error) {
	if runtimeName == "" {
		return false, nil, nil
	}

	imageName := constants.ImageName + ":latest"
	checkCmdArgs := getCheckImageCommandFn(runtimeName)
	checkCmd := exec.Command(checkCmdArgs[0], checkCmdArgs[1:]...)
	checkCmd.Dir = config.GetContainerDir()

	needsRebuild := false
	details := []string{}
	if err := checkCmd.Run(); err != nil {
		needsRebuild = true
		details = append(details, fmt.Sprintf("Image %s missing; rebuilding", imageName))
	} else {
		expected := sha256.Sum256([]byte(templates.Entrypoint))
		expectedHash := hex.EncodeToString(expected[:])

		currentHash, err := imageEntrypointHash(runtimeName, imageName)
		if err != nil {
			needsRebuild = true
			details = append(details, "Unable to verify image entrypoint hash; rebuilding for safety")
			details = append(details, err.Error())
		} else if currentHash != expectedHash {
			needsRebuild = true
			details = append(details, "Image entrypoint is stale; rebuilding")
		}
	}

	if !needsRebuild {
		return false, nil, nil
	}

	cmd, err := buildComposeCommandFn(runtimeName, configPath, "build", []string{"--no-cache"})
	if err != nil {
		return false, details, fmt.Errorf("failed to build image rebuild command: %w", err)
	}
	cmd.Dir = configPath
	cmd.Env = doctorComposeEnv()

	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			return false, details, fmt.Errorf("image rebuild failed: %w", err)
		}
		return false, details, fmt.Errorf("image rebuild failed: %w (%s)", err, msg)
	}

	details = append(details, "Rebuilt construct-box image with updated entrypoint")
	return true, details, nil
}

func imageEntrypointHash(runtimeName, imageName string) (string, error) {
	runtimeCmd := runtimeName
	if runtimeCmd == "container" {
		runtimeCmd = "docker"
	}

	cmd := exec.Command(runtimeCmd, "run", "--rm", "--entrypoint", "sha256sum", imageName, "/usr/local/bin/entrypoint.sh")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	fields := strings.Fields(string(output))
	if len(fields) == 0 {
		return "", fmt.Errorf("unexpected sha256sum output")
	}
	return fields[0], nil
}
