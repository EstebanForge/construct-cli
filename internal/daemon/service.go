// Package daemon manages the optional background daemon container.
package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/ui"
)

const (
	// Service names
	launchdLabel = "com.construct-cli.daemon"
	systemdUnit  = "construct-daemon"
)

// InstallService installs the daemon as a system service that auto-starts on boot.
func InstallService() {
	switch runtime.GOOS {
	case "darwin":
		installLaunchd()
	case "linux":
		installSystemd()
	default:
		ui.GumError(fmt.Sprintf("Auto-start service not supported on %s", runtime.GOOS))
		fmt.Println("You can manually add 'construct sys daemon start' to your startup scripts.")
		os.Exit(1)
	}
}

// UninstallService removes the daemon auto-start service.
func UninstallService() {
	switch runtime.GOOS {
	case "darwin":
		uninstallLaunchd()
	case "linux":
		uninstallSystemd()
	default:
		ui.GumError(fmt.Sprintf("Auto-start service not supported on %s", runtime.GOOS))
		os.Exit(1)
	}
}

// ServiceStatus shows the status of the daemon auto-start service.
func ServiceStatus() {
	switch runtime.GOOS {
	case "darwin":
		statusLaunchd()
	case "linux":
		statusSystemd()
	default:
		fmt.Printf("Auto-start service not supported on %s\n", runtime.GOOS)
	}
}

// --- macOS launchd ---

func getLaunchdPlistPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		// Fallback to current directory if home cannot be determined
		return launchdLabel + ".plist"
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
}

func getConstructBinaryPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "construct"
	}
	return exe
}

func installLaunchd() {
	plistPath := getLaunchdPlistPath()
	binaryPath := getConstructBinaryPath()

	// Ensure LaunchAgents directory exists
	dir := filepath.Dir(plistPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		ui.GumError(fmt.Sprintf("Failed to create LaunchAgents directory: %v", err))
		os.Exit(1)
	}

	// Check if already installed
	if _, err := os.Stat(plistPath); err == nil {
		ui.GumWarning("Daemon service is already installed")
		fmt.Println("Use 'construct sys daemon uninstall' to remove it first")
		os.Exit(1)
	}

	// Create plist content
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>sys</string>
        <string>daemon</string>
        <string>start</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <false/>
    <key>StandardOutPath</key>
    <string>/tmp/construct-daemon.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/construct-daemon.err</string>
</dict>
</plist>
`, launchdLabel, binaryPath)

	// Write plist file
	if err := os.WriteFile(plistPath, []byte(plist), 0644); err != nil {
		ui.GumError(fmt.Sprintf("Failed to write plist file: %v", err))
		os.Exit(1)
	}

	// Load the service
	cmd := exec.Command("launchctl", "load", plistPath)
	if err := cmd.Run(); err != nil {
		ui.GumWarning(fmt.Sprintf("Failed to load service (may need to log out/in): %v", err))
	}

	ui.GumSuccess("Daemon service installed")
	fmt.Println()
	fmt.Println("The daemon will start automatically on login.")
	fmt.Println("To start it now: construct sys daemon start")
	fmt.Println("To remove: construct sys daemon uninstall")
}

func uninstallLaunchd() {
	plistPath := getLaunchdPlistPath()

	// Check if installed
	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		ui.GumWarning("Daemon service is not installed")
		os.Exit(0)
	}

	// Unload the service
	cmd := exec.Command("launchctl", "unload", plistPath)
	if err := cmd.Run(); err != nil {
		ui.LogDebug("Failed to unload service: %v", err)
	}

	// Remove plist file
	if err := os.Remove(plistPath); err != nil {
		ui.GumError(fmt.Sprintf("Failed to remove plist file: %v", err))
		os.Exit(1)
	}

	ui.GumSuccess("Daemon service uninstalled")
	fmt.Println()
	fmt.Println("The daemon will no longer start automatically.")
}

func statusLaunchd() {
	plistPath := getLaunchdPlistPath()

	fmt.Println()
	fmt.Println("=== Daemon Auto-Start Service (macOS) ===")
	fmt.Println()

	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		fmt.Println("Status: Not installed")
		fmt.Println()
		fmt.Println("Install with: construct sys daemon install")
		return
	}

	fmt.Println("Status: Installed")
	fmt.Printf("Plist: %s\n", plistPath)
	fmt.Println()

	// Check if loaded
	cmd := exec.Command("launchctl", "list", launchdLabel)
	output, err := cmd.Output()
	if err != nil {
		fmt.Println("Service state: Not loaded (will start on next login)")
	} else {
		fmt.Printf("Service state: Loaded\n%s\n", strings.TrimSpace(string(output)))
	}
}

// --- Linux systemd ---

func getSystemdUnitPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		// Fallback to current directory if home cannot be determined
		return systemdUnit + ".service"
	}
	return filepath.Join(home, ".config", "systemd", "user", systemdUnit+".service")
}

func installSystemd() {
	unitPath := getSystemdUnitPath()
	binaryPath := getConstructBinaryPath()

	// Ensure systemd user directory exists
	dir := filepath.Dir(unitPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		ui.GumError(fmt.Sprintf("Failed to create systemd user directory: %v", err))
		os.Exit(1)
	}

	// Check if already installed
	if _, err := os.Stat(unitPath); err == nil {
		ui.GumWarning("Daemon service is already installed")
		fmt.Println("Use 'construct sys daemon uninstall' to remove it first")
		os.Exit(1)
	}

	// Create unit file content
	unit := fmt.Sprintf(`[Unit]
Description=Construct CLI Daemon
After=network.target docker.service podman.service
Wants=docker.service

[Service]
Type=oneshot
ExecStart=%s sys daemon start
RemainAfterExit=yes
ExecStop=%s sys daemon stop

[Install]
WantedBy=default.target
`, binaryPath, binaryPath)

	// Write unit file
	if err := os.WriteFile(unitPath, []byte(unit), 0644); err != nil {
		ui.GumError(fmt.Sprintf("Failed to write unit file: %v", err))
		os.Exit(1)
	}

	// Reload systemd
	cmd := exec.Command("systemctl", "--user", "daemon-reload")
	if err := cmd.Run(); err != nil {
		ui.GumWarning(fmt.Sprintf("Failed to reload systemd: %v", err))
	}

	// Enable the service
	cmd = exec.Command("systemctl", "--user", "enable", systemdUnit)
	if err := cmd.Run(); err != nil {
		ui.GumWarning(fmt.Sprintf("Failed to enable service: %v", err))
	}

	ui.GumSuccess("Daemon service installed")
	fmt.Println()
	fmt.Println("The daemon will start automatically on login.")
	fmt.Println("To start it now: construct sys daemon start")
	fmt.Println("To remove: construct sys daemon uninstall")
	fmt.Println()
	fmt.Println("Note: You may need to enable lingering for the service to run without login:")
	fmt.Println("  loginctl enable-linger $USER")
}

func uninstallSystemd() {
	unitPath := getSystemdUnitPath()

	// Check if installed
	if _, err := os.Stat(unitPath); os.IsNotExist(err) {
		ui.GumWarning("Daemon service is not installed")
		os.Exit(0)
	}

	// Stop the service if running
	// Intentionally ignoring error - service might not be running
	cmd := exec.Command("systemctl", "--user", "stop", systemdUnit)
	_ = cmd.Run() //nolint:errcheck // Service might not be running

	// Disable the service
	cmd = exec.Command("systemctl", "--user", "disable", systemdUnit)
	if err := cmd.Run(); err != nil {
		ui.LogDebug("Failed to disable service: %v", err)
	}

	// Remove unit file
	if err := os.Remove(unitPath); err != nil {
		ui.GumError(fmt.Sprintf("Failed to remove unit file: %v", err))
		os.Exit(1)
	}

	// Reload systemd
	cmd = exec.Command("systemctl", "--user", "daemon-reload")
	if err := cmd.Run(); err != nil {
		ui.LogDebug("Failed to reload systemd: %v", err)
	}

	ui.GumSuccess("Daemon service uninstalled")
	fmt.Println()
	fmt.Println("The daemon will no longer start automatically.")
}

func statusSystemd() {
	unitPath := getSystemdUnitPath()

	fmt.Println()
	fmt.Println("=== Daemon Auto-Start Service (Linux systemd) ===")
	fmt.Println()

	if _, err := os.Stat(unitPath); os.IsNotExist(err) {
		fmt.Println("Status: Not installed")
		fmt.Println()
		fmt.Println("Install with: construct sys daemon install")
		return
	}

	fmt.Println("Status: Installed")
	fmt.Printf("Unit file: %s\n", unitPath)
	fmt.Println()

	// Show systemd status
	cmd := exec.Command("systemctl", "--user", "status", systemdUnit, "--no-pager")
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("Status check failed: %v\n", err)
	} else {
		fmt.Println(strings.TrimSpace(string(output)))
	}
}
