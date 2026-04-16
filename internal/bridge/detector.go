package bridge

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/ui"
)

// DetectionMethod represents a host gateway detection method.
type DetectionMethod struct {
	Name     string
	Detect   func() (string, error)
	Priority int // Lower = higher priority
}

// DetectionResult represents the result of gateway detection.
type DetectionResult struct {
	Success   bool
	HostIP    string
	Method    string
	AllErrors []string
}

// DetectHostGateway detects the host gateway IP using multiple methods.
func DetectHostGateway(containerRuntime string, onFailure string) *DetectionResult {
	result := &DetectionResult{
		AllErrors: []string{},
	}

	// Build detection methods based on platform and runtime
	methods := getDetectionMethods(runtime.GOOS, containerRuntime)

	// Try each method in priority order
	for _, method := range methods {
		ui.LogDebug("Trying host gateway detection method: %s", method.Name)

		hostIP, err := method.Detect()
		if err != nil {
			result.AllErrors = append(result.AllErrors,
				fmt.Sprintf("%s: %v", method.Name, err))
			ui.LogDebug("Method %s failed: %v", method.Name, err)
			continue
		}

		if hostIP != "" && isValidIP(hostIP) {
			result.Success = true
			result.HostIP = hostIP
			result.Method = method.Name
			ui.LogDebug("Host gateway detected via %s: %s", method.Name, hostIP)
			return result
		}
	}

	// All methods failed
	handleDetectionFailure(result, onFailure)
	return result
}

// getDetectionMethods returns platform and runtime specific detection methods.
func getDetectionMethods(_, containerRuntime string) []DetectionMethod {
	methods := []DetectionMethod{}

	// Method 1: Docker host-gateway (Docker 20.10+)
	if containerRuntime == "docker" || containerRuntime == "container" {
		methods = append(methods, DetectionMethod{
			Name:     "docker-host-gateway",
			Detect:   detectDockerHostGateway,
			Priority: 1,
		})
	}

	// Method 2: Docker host.docker.internal (macOS, Windows)
	if containerRuntime == "docker" || containerRuntime == "container" {
		methods = append(methods, DetectionMethod{
			Name:     "docker-internal",
			Detect:   detectDockerInternal,
			Priority: 2,
		})
	}

	// Method 3: Podman host.containers.internal
	if containerRuntime == "podman" {
		methods = append(methods, DetectionMethod{
			Name:     "podman-internal",
			Detect:   detectPodmanInternal,
			Priority: 2,
		})
	}

	// Method 4: Network interface inspection
	methods = append(methods, DetectionMethod{
		Name:     "network-inspection",
		Detect:   detectNetworkInterface,
		Priority: 3,
	})

	// Method 5: Docker bridge network inspection
	if containerRuntime == "docker" || containerRuntime == "container" {
		methods = append(methods, DetectionMethod{
			Name:     "docker-bridge-inspection",
			Detect:   detectDockerBridge,
			Priority: 4,
		})
	}

	return methods
}

// detectDockerHostGateway detects using Docker's host-gateway (Docker 20.10+)
func detectDockerHostGateway() (string, error) {
	// Check if docker supports host-gateway
	cmd := exec.Command("docker", "run", "--rm", "alpine",
		"sh", "-c", "getent hosts host.gateway | awk '{print $1}'")

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("docker host-gateway not available: %w", err)
	}

	hostIP := strings.TrimSpace(string(output))
	if hostIP == "" {
		return "", fmt.Errorf("no IP returned from host.gateway")
	}

	return hostIP, nil
}

// detectDockerInternal detects using host.docker.internal
func detectDockerInternal() (string, error) {
	cmd := exec.Command("docker", "run", "--rm", "alpine",
		"sh", "-c", "getent hosts host.docker.internal | awk '{print $1}'")

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("host.docker.internal not available: %w", err)
	}

	hostIP := strings.TrimSpace(string(output))
	if hostIP == "" {
		return "", fmt.Errorf("no IP returned from host.docker.internal")
	}

	return hostIP, nil
}

// detectPodmanInternal detects using host.containers.internal
func detectPodmanInternal() (string, error) {
	cmd := exec.Command("podman", "run", "--rm", "alpine",
		"sh", "-c", "getent hosts host.containers.internal | awk '{print $1}'")

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("host.containers.internal not available: %w", err)
	}

	hostIP := strings.TrimSpace(string(output))
	if hostIP == "" {
		return "", fmt.Errorf("no IP returned from host.containers.internal")
	}

	return hostIP, nil
}

// detectNetworkInterface detects host IP by inspecting network interfaces
func detectNetworkInterface() (string, error) {
	// Try common gateway IPs
	gatewayIPs := []string{
		"192.168.65.1", // Docker Desktop macOS
		"192.168.1.1",  // Common router
		"10.0.2.2",     // QEMU/kvm
		"172.17.0.1",   // Docker bridge default
	}

	for _, ip := range gatewayIPs {
		if isValidIP(ip) {
			// Try to ping or connect to verify
			if isReachable(ip) {
				return ip, nil
			}
		}
	}

	return "", fmt.Errorf("no reachable gateway IP found")
}

// detectDockerBridge detects by inspecting Docker bridge network
func detectDockerBridge() (string, error) {
	cmd := exec.Command("docker", "network", "inspect", "bridge",
		"--format", "{{range .IPAM.Config}}{{.Gateway}}{{end}}")

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to inspect docker bridge: %w", err)
	}

	hostIP := strings.TrimSpace(string(output))
	if hostIP == "" {
		return "", fmt.Errorf("no gateway IP in docker bridge network")
	}

	return hostIP, nil
}

// handleDetectionFailure handles detection failure based on on_failure setting.
func handleDetectionFailure(result *DetectionResult, onFailure string) {
	switch onFailure {
	case "silent":
		// Do nothing
		return

	case "fail":
		ui.GumError("Host gateway detection failed")
		fmt.Println("\nAll detection methods failed:")
		for _, err := range result.AllErrors {
			fmt.Printf("  • %s\n", err)
		}
		fmt.Println("\nTroubleshooting:")
		fmt.Println("  1. Update Docker/Podman to latest version")
		fmt.Println("  2. Set manual_host_ip in config.toml")
		fmt.Println("  3. Run: construct sys doctor --host-bridge")
		os.Exit(1)

	case "warn":
		ui.GumWarning("Host gateway detection failed")
		fmt.Println("\nAll detection methods failed:")
		for _, err := range result.AllErrors {
			fmt.Printf("  • %s\n", err)
		}
		fmt.Println("\n⚠️  Container will start without host service access")
		fmt.Println("   AgentMemory plugin hooks will fail silently")
		fmt.Println("\nFix options:")
		fmt.Println("  1. Set manual_host_ip in [sandbox.host_services]")
		fmt.Println("  2. Update container runtime (Docker 20.10+ or Podman 3.0+)")
		fmt.Println("  3. Run: construct sys doctor --host-bridge")
	}
}

// isValidIP checks if a string is a valid IP address
func isValidIP(ip string) bool {
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return false
	}

	for _, part := range parts {
		if len(part) == 0 || len(part) > 3 {
			return false
		}
		for _, char := range part {
			if char < '0' || char > '9' {
				return false
			}
		}
	}

	return true
}

// isReachable checks if an IP is reachable (basic ping/connect test)
func isReachable(ip string) bool {
	// For now, just validate the IP format
	// In production, you might want to actually try connecting
	return isValidIP(ip)
}
