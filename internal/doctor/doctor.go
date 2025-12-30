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
	runtimepkg "github.com/EstebanForge/construct-cli/internal/runtime"
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
func Run() {
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

	var checks []CheckResult

	// 1. Runtime Check
	runtimeCheck := CheckResult{Name: "Container Runtime"}
	cfg, _, err := config.Load() // Ignore error here, we check config file later
	if err != nil {
		ui.LogWarning("Failed to load config: %v", err)
	}
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
	checks = append(checks, runtimeCheck)

	// 2. Config Check
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

	// 3. Setup Log Check
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

	// 4. Templates Check
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

	// 5. Packages Config Check
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

	// 6. Entrypoint State Check
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

	// 7. Image Check
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

	// 8. Agents Installation Check
	volumeCheck := CheckResult{Name: "Agent Installation"}
	if cfg != nil && hasAgentBinaries(config.GetConfigDir()) {
		volumeCheck.Status = CheckStatusOK
		volumeCheck.Message = "Agent tools installed"
	} else {
		volumeCheck.Status = CheckStatusWarning
		volumeCheck.Message = "Agents not found (will install on first run)"
	}
	checks = append(checks, volumeCheck)

	// 9. SSH Agent Check
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

	// 10. SSH Keys Check (Imported)
	keysCheck := CheckResult{Name: "Local SSH Keys"}
	sshDir := filepath.Join(config.GetConfigDir(), "home", ".ssh")
	if entries, err := os.ReadDir(sshDir); err == nil && len(entries) > 0 {
		var keyNames []string
		for _, entry := range entries {
			if !entry.IsDir() && !strings.HasSuffix(entry.Name(), ".pub") {
				keyNames = append(keyNames, entry.Name())
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

	// 11. Clipboard Bridge Check
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

func hasAgentBinaries(configDir string) bool {
	binDir := filepath.Join(configDir, "home", ".local", "bin")
	candidates := []string{
		"claude",
		"mcp-cli-ent",
		"opencode",
		"gemini",
		"codex",
		"qwen-code",
	}

	for _, name := range candidates {
		if _, err := os.Stat(filepath.Join(binDir, name)); err == nil {
			return true
		}
	}

	return false
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
