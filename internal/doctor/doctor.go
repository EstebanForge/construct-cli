// Package doctor provides system health checks.
package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/runtime"
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

	runtimeName := runtime.DetectRuntime(engine)
	if runtimeName != "" {
		runtimeCheck.Status = CheckStatusOK
		runtimeCheck.Message = fmt.Sprintf("Found %s", runtimeName)
		// Check version/status
		if runtime.IsRuntimeRunning(runtimeName) {
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

	// 3. Image Check
	imageCheck := CheckResult{Name: "Construct Image"}
	checkCmdArgs := runtime.GetCheckImageCommand(runtimeName)
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

	// 4. Agents Volume Check
	volumeCheck := CheckResult{Name: "Agent Installation"}
	if cfg != nil && runtime.AreAgentsInstalled(cfg) {
		volumeCheck.Status = CheckStatusOK
		volumeCheck.Message = "Agents installed in persistent volume"
	} else {
		volumeCheck.Status = CheckStatusWarning
		volumeCheck.Message = "Agents not found (will install on first run)"
	}
	checks = append(checks, volumeCheck)

	// 5. SSH Agent Check
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

	// 6. SSH Keys Check (Imported)
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

	// Print Report
	for _, check := range checks {
		printCheckResult(check)
	}

	fmt.Println()
}

func printCheckResult(check CheckResult) {
	statusIcon := "✓"
	color := "212" // Green/Pinkish

	switch check.Status {
	case CheckStatusWarning:
		statusIcon = "!"
		color = "214" // Orange
	case CheckStatusError:
		statusIcon = "✗"
		color = "196" // Red
	case CheckStatusSkipped:
		statusIcon = "-"
		color = "242" // Grey
	}

	if ui.GumAvailable() {
		// Use gum for status
		cmd := exec.Command("gum", "style", "--foreground", color, fmt.Sprintf("%s %s: %s", statusIcon, check.Name, check.Message))
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to render check: %v\n", err)
		}

		for _, detail := range check.Details {
			cmd := exec.Command("gum", "style", "--foreground", "242", fmt.Sprintf("  • %s", detail))
			cmd.Stdout = os.Stdout
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to render detail: %v\n", err)
			}
		}

		if check.Suggestion != "" {
			cmd := exec.Command("gum", "style", "--foreground", "214", "--italic", fmt.Sprintf("  → Suggestion: %s", check.Suggestion))
			cmd.Stdout = os.Stdout
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to render suggestion: %v\n", err)
			}
		}
	} else {
		fmt.Printf("[%s] %s: %s\n", check.Status, check.Name, check.Message)
		for _, detail := range check.Details {
			fmt.Printf("  • %s\n", detail)
		}
		if check.Suggestion != "" {
			fmt.Printf("  → Suggestion: %s\n", check.Suggestion)
		}
	}
}
