package sys

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/EstebanForge/construct-cli/internal/config"
)

const defaultLoginBridgePorts = "1455,8085"
const loginBridgeFlagFile = ".login_bridge"

// LoginBridge enables localhost login callback forwarding until interrupted.
func LoginBridge(args []string) {
	fs := flag.NewFlagSet("login-bridge", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	ports := fs.String("ports", defaultLoginBridgePorts, "Comma-separated callback ports for login flows")

	if err := fs.Parse(args); err != nil {
		fmt.Println("Usage: construct sys login-bridge [--ports 1455,8085]")
		return
	}

	normalized := normalizePortList(*ports)
	if normalized == "" {
		normalized = defaultLoginBridgePorts
	}

	if err := os.MkdirAll(config.GetConfigDir(), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to create config directory: %v\n", err)
		return
	}

	flagPath := filepath.Join(config.GetConfigDir(), loginBridgeFlagFile)
	if err := os.WriteFile(flagPath, []byte(fmt.Sprintf("%s\n", normalized)), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to enable login bridge: %v\n", err)
		return
	}
	defer func() {
		if err := os.Remove(flagPath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: Failed to remove login bridge flag: %v\n", err)
		}
	}()

	fmt.Printf("Login bridge active on localhost ports: %s\n", normalized)
	fmt.Println("Run your agent login in another terminal window.")
	fmt.Println("Press Enter to stop the bridge.")

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)

	inputChan := make(chan struct{}, 1)
	go func() {
		reader := bufio.NewReader(os.Stdin)
		if _, err := reader.ReadString('\n'); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to read input: %v\n", err)
		}
		inputChan <- struct{}{}
	}()

	select {
	case <-signalChan:
	case <-inputChan:
	}

	fmt.Println("Login bridge stopped.")
}

func normalizePortList(raw string) string {
	ports := parsePorts(raw)
	if len(ports) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ports))
	for _, port := range ports {
		parts = append(parts, fmt.Sprintf("%d", port))
	}
	return strings.Join(parts, ",")
}

func parsePorts(raw string) []int {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	seen := make(map[int]struct{})
	ports := make([]int, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		port, err := strconv.Atoi(part)
		if err != nil || port <= 0 {
			continue
		}
		if _, exists := seen[port]; exists {
			continue
		}
		seen[port] = struct{}{}
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports
}
