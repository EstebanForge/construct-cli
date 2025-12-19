package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/runtime"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

// CheckStatus represents the result of a health check
type CheckStatus string

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

// DoctorReport contains all health check results
type DoctorReport struct {
	Checks      []CheckResult
	Summary     string
	HasErrors   bool
	HasWarnings bool
}

// RunDoctor performs system health checks and prints a report
func Run() {
	fmt.Println()
	if ui.GumAvailable() {
		cmd := exec.Command("gum", "style", "--border", "rounded", "--padding", "1 2", "--bold", "The Construct Doctor")
		cmd.Stdout = os.Stdout
		cmd.Run()
	} else {
		fmt.Println("=== The Construct Doctor ===")
	}
	fmt.Println()

	var checks []CheckResult

	// 1. Runtime Check
	runtimeCheck := CheckResult{Name: "Container Runtime"}
	cfg, _, _ := config.Load() // Ignore error here, we check config file later
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
		cmd.Run()

		for _, detail := range check.Details {
			cmd := exec.Command("gum", "style", "--foreground", "242", fmt.Sprintf("  • %s", detail))
			cmd.Stdout = os.Stdout
			cmd.Run()
		}

		if check.Suggestion != "" {
			cmd := exec.Command("gum", "style", "--foreground", "214", "--italic", fmt.Sprintf("  → Suggestion: %s", check.Suggestion))
			cmd.Stdout = os.Stdout
			cmd.Run()
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
