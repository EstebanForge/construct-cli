// Package doctor provides system health checks.
package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/constants"
	runtimepkg "github.com/EstebanForge/construct-cli/internal/runtime"
	"github.com/EstebanForge/construct-cli/internal/sys"
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

// Run performs system health checks and prints a report.
func Run(args ...string) {
	fmt.Println()
	if ui.GumAvailable() {
		cmd := exec.Command("gum", "style", "--border", "rounded", "--padding", "1 2", "--bold", "The Construct Doctor")
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render header: %v\n", err)
		}
	} else {
		fmt.Println("=== The Construct Doctor ===")
	}
	fmt.Println()

	checks := make([]CheckResult, 0, 15)

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
		if cfg != nil && !cfg.Sandbox.NonRootStrict {
			envCheck.Status = CheckStatusWarning
			envCheck.Message = "Manual container user mapping detected"
			envCheck.Suggestion = "Remove 'user:' from docker-compose.override.yml for Docker, or set [sandbox].non_root_strict = true if strict non-root is required"
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
	// If config load failed, we might use default engine "auto"
	engine := "auto"
	if cfg != nil {
		engine = cfg.Runtime.Engine
	}

	runtimeName := runtimepkg.DetectRuntime(engine)
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

	fixRequested := false
	for _, arg := range args {
		if arg == "--fix" {
			fixRequested = true
		}
	}

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
		configDir := config.GetConfigDir()
		paths := []string{
			filepath.Join(configDir, "home"),
			filepath.Join(configDir, "container"),
		}
		missing := false
		notWritable := false
		for _, path := range paths {
			if _, err := os.Stat(path); err != nil {
				if os.IsNotExist(err) {
					missing = true
					permCheck.Details = append(permCheck.Details, fmt.Sprintf("Missing: %s", path))
					continue
				}
				notWritable = true
				permCheck.Details = append(permCheck.Details, fmt.Sprintf("Error: %s (%v)", path, err))
				continue
			}
			if ok, err := isWritableDir(path); !ok {
				notWritable = true
				permCheck.Details = append(permCheck.Details, fmt.Sprintf("Not writable: %s (%v)", path, err))
			}
		}
		if missing {
			permCheck.Status = CheckStatusWarning
			permCheck.Message = "Config directories missing"
			permCheck.Suggestion = "Run 'construct sys init' or 'construct sys migrate'"
		} else if notWritable {
			permCheck.Status = CheckStatusWarning
			permCheck.Message = "Config directories not writable"
			permCheck.Suggestion = "Fix ownership: sudo chown -R $USER:$USER ~/.config/construct-cli"
		} else {
			permCheck.Status = CheckStatusOK
			permCheck.Message = "Config directories writable"
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
		templatesCheck.Suggestion = "Run 'construct sys migrate' to refresh templates"
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
