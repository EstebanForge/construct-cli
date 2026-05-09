package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	stdruntime "runtime"
	"slices"
	"strings"
	"time"

	"github.com/EstebanForge/construct-cli/internal/clipboard"
	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/constants"
	"github.com/EstebanForge/construct-cli/internal/env"
	"github.com/EstebanForge/construct-cli/internal/network"
	"github.com/EstebanForge/construct-cli/internal/runtime"
	"github.com/EstebanForge/construct-cli/internal/security"
	"github.com/EstebanForge/construct-cli/internal/ui"
)

const defaultLoginForwardPorts = "1455,8085"
const loginForwardListenOffset = 10000
const loginBridgeFlagFile = ".login_bridge"
const daemonSSHProxySock = "/home/construct/.ssh/agent.sock"

var startClipboardServerFn = clipboard.StartServer
var execInteractiveAsUserFn = runtime.ExecInteractiveAsUser

// RuntimeEngine handles the high-level orchestration of agent execution.
type RuntimeEngine struct {
	cfg              *config.Config
	args             []string
	containerRuntime string
	configPath       string
	providerEnv      []string

	// Internal state
	sec          security.Session
	prepared     bool
	sshBridge    *SSHBridge
	cbServer     *clipboard.Server
	loginForward bool
	loginPorts   []int
	osEnv        []string
	cwd          string
}

// NewRuntimeEngine creates a new runtime engine.
func NewRuntimeEngine(cfg *config.Config, args []string, containerRuntime, configPath string, providerEnv []string) *RuntimeEngine {
	return &RuntimeEngine{
		cfg:              cfg,
		args:             args,
		containerRuntime: containerRuntime,
		configPath:       configPath,
		providerEnv:      providerEnv,
	}
}

// Prepare handles environment setup, security initialization, and platform bridges.
func (e *RuntimeEngine) Prepare() error {
	// 1. Security Initialization
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	sec, err := security.InitializeSessionForRun(e.cfg, e.configPath, cwd)
	if err != nil {
		return fmt.Errorf("failed to initialize security session: %w", err)
	}
	e.sec = sec
	e.cwd = sec.ProjectRoot()

	// 2. Directory Preparation
	if err := e.ensureAgentRuntimeDirs(); err != nil {
		return err
	}

	// 3. Environment Preparation
	e.osEnv = os.Environ()
	env.SetEnvVar(&e.osEnv, "PWD", e.cwd)
	e.osEnv = runtime.AppendProjectPathEnv(e.osEnv)
	e.osEnv = network.InjectEnv(e.osEnv, e.cfg)
	e.osEnv = runtime.AppendRuntimeIdentityEnv(e.osEnv, e.containerRuntime)
	applyConstructPath(&e.osEnv)

	// 4. Login Bridge Detection
	e.loginForward, e.loginPorts = shouldEnableLoginForward(e.args)

	// 5. Clipboard Server
	clipboardHost := "host.docker.internal"
	if e.cfg != nil && e.cfg.Sandbox.ClipboardHost != "" {
		clipboardHost = e.cfg.Sandbox.ClipboardHost
	}
	cbServer, err := startClipboardServerFn(clipboardHost)
	if err == nil {
		e.cbServer = cbServer
	} else {
		if ui.CurrentLogLevel >= ui.LogLevelInfo {
			fmt.Printf("Warning: Failed to start clipboard server: %v\n", err)
		}
	}

	// 6. Platform Bridges (macOS SSH Agent Forwarding)
	if stdruntime.GOOS == "darwin" && e.cfg.Sandbox.ForwardSSHAgent {
		if os.Getenv("SSH_AUTH_SOCK") != "" {
			bridge, err := StartSSHBridge()
			if err == nil {
				e.sshBridge = bridge
			}
		}
	}

	e.prepared = true
	return nil
}

// Execute handles the actual agent execution, choosing between daemon or direct run.
func (e *RuntimeEngine) Execute() (int, error) {
	if !e.prepared {
		return 1, fmt.Errorf("engine not prepared")
	}

	baseArgs := e.args
	mergedProviderEnv := collectForwardedEnv(e.cfg, e.providerEnv)

	daemonName := "construct-cli-daemon"
	daemonState := runtime.GetContainerState(e.containerRuntime, daemonName)

	// 1. Try Daemon Path
	if daemonState == runtime.ContainerStateRunning {
		daemonArgs := applyYoloArgs(baseArgs, e.cfg)
		if ok, exitCode, err := e.execViaDaemon(daemonArgs, daemonName, mergedProviderEnv); ok {
			return exitCode, err
		}
	} else if e.cfg.Daemon.AutoStart {
		if daemonState == runtime.ContainerStateExited {
			_ = runtime.CleanupExitedContainer(e.containerRuntime, daemonName) //nolint:errcheck
		}

		if e.startDaemonBackground(daemonName) {
			if e.waitForDaemon(daemonName, 10) {
				daemonArgs := applyYoloArgs(baseArgs, e.cfg)
				if ok, exitCode, err := e.execViaDaemon(daemonArgs, daemonName, mergedProviderEnv); ok {
					return exitCode, err
				}
			}
		}
	}

	// 2. Direct Container Path
	args := applyYoloArgs(baseArgs, e.cfg)
	containerName := "construct-cli"
	state := runtime.GetContainerState(e.containerRuntime, containerName)

	switch state {
	case runtime.ContainerStateRunning:
		fmt.Printf("⚠️  Container '%s' is already running.\n\n", containerName)
		choice, err := e.promptForAttachOrRestart(containerName)
		if err != nil {
			return 0, nil // Canceled
		}
		if choice == "attach" {
			return e.execInRunningContainer(args, containerName, mergedProviderEnv)
		}
		// Continue to "Stop and Restart"
		_ = runtime.StopContainer(e.containerRuntime, containerName)          //nolint:errcheck
		_ = runtime.CleanupExitedContainer(e.containerRuntime, containerName) //nolint:errcheck

	case runtime.ContainerStateExited:
		fmt.Printf("🧹 Removing old stopped container '%s'...\n", containerName)
		_ = runtime.CleanupExitedContainer(e.containerRuntime, containerName) //nolint:errcheck
	}

	// 3. Final Build & Run
	e.ensureImageExists()
	return e.runNewContainer(args, containerName, mergedProviderEnv)
}

// Teardown cleans up resources used by the engine.
func (e *RuntimeEngine) Teardown() {
	if e.sshBridge != nil {
		e.sshBridge.Stop()
	}
	if e.cbServer != nil {
		e.cbServer.Stop()
	}
	if e.sec != nil {
		e.sec.Close() //nolint:errcheck
	}
}

func (e *RuntimeEngine) ensureAgentRuntimeDirs() error {
	if len(e.args) == 0 || e.configPath == "" {
		return nil
	}

	agent := strings.ToLower(e.args[0])
	switch agent {
	case "codex":
		codexHome := filepath.Join(e.configPath, "home", ".codex")
		if err := os.MkdirAll(codexHome, 0755); err != nil {
			return fmt.Errorf("failed to create Codex home directory at %s: %w", codexHome, err)
		}
	case "opencode":
		opencodeDataDir := filepath.Join(e.configPath, "home", ".local", "share", "opencode")
		if err := os.MkdirAll(opencodeDataDir, 0755); err != nil {
			return fmt.Errorf("failed to create OpenCode data directory at %s: %w", opencodeDataDir, err)
		}
		opencodeConfigDir := filepath.Join(e.configPath, "home", ".config", "opencode")
		if err := os.MkdirAll(opencodeConfigDir, 0755); err != nil {
			return fmt.Errorf("failed to create OpenCode config directory at %s: %w", opencodeConfigDir, err)
		}
	}
	return nil
}

func (e *RuntimeEngine) execViaDaemon(args []string, daemonName string, providerEnv []string) (bool, int, error) {
	imageName := constants.ImageName + ":latest"
	if runtime.IsContainerStale(e.containerRuntime, daemonName, imageName) {
		fmt.Println("⚠️  Daemon is running an outdated image. Falling back to normal startup...")
		return false, 0, nil
	}

	daemonMounts := runtime.ResolveDaemonMounts(e.cfg)
	workdir := ""
	if daemonMounts.Enabled {
		label, _ := runtime.GetContainerLabel(e.containerRuntime, daemonName, runtime.DaemonMountsLabelKey) //nolint:errcheck
		if label != daemonMounts.Hash {
			return false, 0, nil
		}

		var ok bool
		workdir, ok = runtime.MapDaemonWorkdirFromMounts(e.cwd, daemonMounts.Mounts)
		if !ok {
			e.warnDaemonMountFallback()
			return false, 0, nil
		}
	} else {
		daemonWorkdir, err := runtime.GetContainerWorkingDir(e.containerRuntime, daemonName)
		if err != nil {
			return false, 0, nil
		}

		mountSource, err := runtime.GetContainerMountSource(e.containerRuntime, daemonName, daemonWorkdir)
		if err != nil {
			return false, 0, nil
		}

		var ok bool
		workdir, ok = mapDaemonWorkdir(e.cwd, mountSource, daemonWorkdir)
		if !ok {
			e.warnDaemonMountFallback()
			return false, 0, nil
		}
	}

	// Setup SSH Bridge Proxy if needed
	execUser := resolveExecUserForRunningContainer(e.cfg, e.containerRuntime, daemonName)
	var bridgeEnv []string
	if e.sshBridge != nil {
		if err := e.ensureDaemonSSHProxy(daemonName, e.sshBridge.Port, execUser); err == nil {
			_ = e.waitForDaemonSSHProxy(daemonName, execUser) //nolint:errcheck
			bridgeEnv = []string{
				fmt.Sprintf("CONSTRUCT_SSH_BRIDGE_PORT=%d", e.sshBridge.Port),
				"SSH_AUTH_SOCK=" + daemonSSHProxySock,
			}
			fmt.Println("✓ Started SSH Agent proxy (daemon)")
		}
	}

	envVars := buildDaemonExecEnv(args, providerEnv, e.cbServer, e.cfg)
	for _, bev := range bridgeEnv {
		parts := strings.SplitN(bev, "=", 2)
		if len(parts) == 2 {
			env.SetEnvVar(&envVars, parts[0], parts[1])
		}
	}

	// Ensure Construct home and path
	applyConstructPath(&envVars)
	env.SetEnvVar(&envVars, "HOME", "/home/construct")

	// Patch the daemon if needed (ensure clipboard shims and wrappers are present)
	if e.cfg == nil || e.cfg.Agents.ClipboardImagePatch {
		_ = e.runAgentPatchInDaemon(daemonName, execUser) //nolint:errcheck
	}

	envVars = e.sec.MaskEnv(envVars)

	// Default to configured shell when no command is provided.
	if len(args) == 0 {
		shell := "/bin/bash"
		if e.cfg != nil && e.cfg.Sandbox.Shell != "" {
			shell = e.cfg.Sandbox.Shell
		}
		args = []string{shell}
		fmt.Println("Entering Construct daemon shell...")
	} else {
		fmt.Printf("Running in Construct daemon: %v\n", args)
	}

	exitCode, err := execInteractiveAsUserFn(e.containerRuntime, daemonName, args, envVars, workdir, execUser)
	if err == nil && len(args) > 0 && (exitCode == 126 || exitCode == 127) {
		fmt.Printf("Hint: command '%s' may be missing from daemon PATH.\n", args[0])
		fmt.Println("Run 'construct sys doctor' and review Setup/Update logs for package installation errors.")
		fmt.Println("If needed, run 'construct sys packages --install' to reapply packages.toml.")
	}
	return true, exitCode, err
}

func (e *RuntimeEngine) startDaemonBackground(daemonName string) bool {
	// Check if image exists first
	checkCmdArgs := runtime.GetCheckImageCommand(e.containerRuntime)
	checkCmd := exec.Command(checkCmdArgs[0], checkCmdArgs[1:]...)
	checkCmd.Dir = config.GetContainerDir()
	if err := checkCmd.Run(); err != nil {
		return false
	}

	daemonMounts := runtime.ResolveDaemonMounts(e.cfg)
	if e.cfg != nil && e.cfg.Daemon.MultiPathsEnabled && !daemonMounts.Enabled {
		if ui.CurrentLogLevel >= ui.LogLevelInfo {
			fmt.Println("Warning: daemon.multi_paths_enabled is true but no valid daemon.mount_paths were found; skipping daemon auto-start.")
		}
		return false
	}

	fmt.Println("🚀 Starting daemon for faster subsequent runs...")

	osEnv := os.Environ()
	env.SetEnvVar(&osEnv, "PWD", e.cwd)
	osEnv = runtime.AppendProjectPathEnv(osEnv)
	osEnv = network.InjectEnv(osEnv, e.cfg)
	osEnv = runtime.AppendRuntimeIdentityEnv(osEnv, e.containerRuntime)
	applyConstructPath(&osEnv)

	cmd, err := runtime.BuildComposeCommand(e.containerRuntime, e.configPath, "run", []string{"-d", "--rm", "--name", daemonName, "construct-box"})
	if err != nil {
		return false
	}

	cmd.Dir = config.GetContainerDir()
	cmd.Env = osEnv

	if err := cmd.Run(); err != nil {
		return false
	}

	return true
}

func (e *RuntimeEngine) waitForDaemon(daemonName string, timeoutSeconds int) bool {
	for i := 0; i < timeoutSeconds*2; i++ {
		if runtime.GetContainerState(e.containerRuntime, daemonName) == runtime.ContainerStateRunning {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

func (e *RuntimeEngine) execInRunningContainer(args []string, containerName string, providerEnv []string) (int, error) {
	if len(args) == 0 {
		shell := "/bin/bash"
		if e.cfg != nil && e.cfg.Sandbox.Shell != "" {
			shell = e.cfg.Sandbox.Shell
		}
		args = []string{shell}
	}

	clipboardPatchValue := "1"
	if e.cfg != nil && !e.cfg.Agents.ClipboardImagePatch {
		clipboardPatchValue = "0"
	}

	envVars := providerEnv
	if len(args) > 0 {
		env.SetEnvVar(&envVars, "CONSTRUCT_AGENT_NAME", args[0])
		appendAgentSpecificExecEnv(&envVars, args[0], clipboardPatchValue)
	}

	env.SetEnvVar(&envVars, "CONSTRUCT_CLIPBOARD_IMAGE_PATCH", clipboardPatchValue)

	if clipboardPatchValue != "0" {
		display := ":0"
		if d := os.Getenv("CONSTRUCT_X11_DISPLAY"); d != "" {
			display = d
		}
		env.SetEnvVar(&envVars, "DISPLAY", display)
	}

	if os.Getenv("CONSTRUCT_DEBUG") == "1" {
		env.SetEnvVar(&envVars, "CONSTRUCT_DEBUG", "1")
	}

	if colorterm := os.Getenv("COLORTERM"); colorterm != "" {
		env.SetEnvVar(&envVars, "COLORTERM", colorterm)
	} else {
		env.SetEnvVar(&envVars, "COLORTERM", "truecolor")
	}

	if e.cbServer != nil {
		env.SetEnvVar(&envVars, "CONSTRUCT_CLIPBOARD_URL", e.cbServer.URL)
		env.SetEnvVar(&envVars, "CONSTRUCT_CLIPBOARD_TOKEN", e.cbServer.Token)
		env.SetEnvVar(&envVars, "CONSTRUCT_FILE_PASTE_AGENTS", constants.FileBasedPasteAgents)
	}

	// On Linux, we set SSH_AUTH_SOCK to the container mount point /ssh-agent
	if stdruntime.GOOS == "linux" {
		env.SetEnvVar(&envVars, "SSH_AUTH_SOCK", "/ssh-agent")
	}

	// Ensure Construct home and path
	applyConstructPath(&envVars)
	env.SetEnvVar(&envVars, "HOME", "/home/construct")

	execUser := resolveExecUserForRunningContainer(e.cfg, e.containerRuntime, containerName)
	envVars = e.sec.MaskEnv(envVars)

	return execInteractiveAsUserFn(e.containerRuntime, containerName, args, envVars, "", execUser)
}

func (e *RuntimeEngine) runNewContainer(_ []string, containerName string, providerEnv []string) (int, error) {
	runFlags := []string{"--name", containerName}
	e.buildRunFlags(&runFlags, providerEnv)

	// Final environment masking
	for i, val := range runFlags {
		if val == "-e" && i+1 < len(runFlags) {
			masked := e.sec.MaskEnv([]string{runFlags[i+1]})
			runFlags[i+1] = masked[0]
		}
	}

	cmd, err := runtime.BuildComposeCommand(e.containerRuntime, e.configPath, "run", runFlags)
	if err != nil {
		return 1, err
	}

	cmd.Dir = config.GetContainerDir()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}

func (e *RuntimeEngine) buildRunFlags(runFlags *[]string, providerEnv []string) {
	*runFlags = append(*runFlags, "--rm")
	if stdruntime.GOOS == "darwin" {
		*runFlags = append(*runFlags, "--user", "construct")
	}
	appendExecUserRunFlags(runFlags, e.cfg, e.containerRuntime)

	if e.loginForward {
		for _, port := range e.loginPorts {
			listenPort := port + loginForwardListenOffset
			*runFlags = append(*runFlags, "-p", fmt.Sprintf("127.0.0.1:%d:%d", port, listenPort))
		}
	}

	if e.cbServer != nil {
		*runFlags = append(*runFlags, "-e", "CONSTRUCT_CLIPBOARD_URL="+e.cbServer.URL)
		*runFlags = append(*runFlags, "-e", "CONSTRUCT_CLIPBOARD_TOKEN="+e.cbServer.Token)
		*runFlags = append(*runFlags, "-e", "CONSTRUCT_FILE_PASTE_AGENTS="+constants.FileBasedPasteAgents)
	}

	clipboardPatchValue := "1"
	if e.cfg != nil && !e.cfg.Agents.ClipboardImagePatch {
		clipboardPatchValue = "0"
	}
	*runFlags = append(*runFlags, "-e", "CONSTRUCT_CLIPBOARD_IMAGE_PATCH="+clipboardPatchValue)

	if clipboardPatchValue != "0" {
		display := ":0"
		if d := os.Getenv("CONSTRUCT_X11_DISPLAY"); d != "" {
			display = d
		}
		*runFlags = append(*runFlags, "-e", "DISPLAY="+display)
	}

	if os.Getenv("CONSTRUCT_DEBUG") == "1" {
		*runFlags = append(*runFlags, "-e", "CONSTRUCT_DEBUG=1")
	}

	if colorterm := os.Getenv("COLORTERM"); colorterm != "" {
		*runFlags = append(*runFlags, "-e", "COLORTERM="+colorterm)
	} else {
		*runFlags = append(*runFlags, "-e", "COLORTERM=truecolor")
	}

	if len(e.args) > 0 {
		*runFlags = append(*runFlags, "-e", "CONSTRUCT_AGENT_NAME="+e.args[0])
		appendAgentSpecificRunFlags(runFlags, e.args[0], clipboardPatchValue)
	}

	for _, ev := range e.osEnv {
		if strings.HasPrefix(ev, "CONSTRUCT_SSH_BRIDGE_PORT=") {
			*runFlags = append(*runFlags, "-e", ev)
			break
		}
	}

	if e.loginForward {
		*runFlags = append(*runFlags, "-e", "CONSTRUCT_LOGIN_FORWARD=1")
		*runFlags = append(*runFlags, "-e", "CONSTRUCT_LOGIN_FORWARD_PORTS="+formatPorts(e.loginPorts))
		*runFlags = append(*runFlags, "-e", fmt.Sprintf("CONSTRUCT_LOGIN_FORWARD_LISTEN_OFFSET=%d", loginForwardListenOffset))
	}

	for _, envVar := range providerEnv {
		*runFlags = append(*runFlags, "-e", envVar)
	}

	for _, ev := range e.osEnv {
		if strings.HasPrefix(ev, "PATH=") {
			*runFlags = append(*runFlags, "-e", ev)
			break
		}
	}

	// Always inject HOME and CONSTRUCT_PATH for agents
	constructPath := env.BuildConstructPath("/home/construct")
	*runFlags = append(*runFlags, "-e", "HOME=/home/construct")
	*runFlags = append(*runFlags, "-e", "CONSTRUCT_PATH="+constructPath)

	*runFlags = append(*runFlags, "construct-box")
	*runFlags = append(*runFlags, e.args...)
}

func (e *RuntimeEngine) ensureDaemonSSHProxy(daemonName string, port int, execUser string) error {
	envVars := []string{fmt.Sprintf("CONSTRUCT_SSH_BRIDGE_PORT=%d", port)}
	cmdArgs := []string{"bash", "-lc", `if ! command -v socat >/dev/null; then echo "socat not found" >&2; exit 1; fi; PROXY_SOCK="` + daemonSSHProxySock + `"; PROXY_DIR="$(dirname "$PROXY_SOCK")"; mkdir -p "$PROXY_DIR" 2>/dev/null || true; chmod 700 "$PROXY_DIR" 2>/dev/null || true; rm -f "$PROXY_SOCK"; nohup socat UNIX-LISTEN:"$PROXY_SOCK",fork,mode=600 TCP:host.docker.internal:"$CONSTRUCT_SSH_BRIDGE_PORT" >/tmp/socat.log 2>&1 &`}
	_, err := runtime.ExecInContainerWithEnv(e.containerRuntime, daemonName, cmdArgs, envVars, execUser)
	return err
}

func (e *RuntimeEngine) waitForDaemonSSHProxy(daemonName, execUser string) error {
	for i := 0; i < 10; i++ {
		if err := e.checkDaemonSSHProxy(daemonName, execUser); err == nil {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("SSH agent proxy not ready")
}

func (e *RuntimeEngine) checkDaemonSSHProxy(daemonName, execUser string) error {
	cmdArgs := []string{"bash", "-lc", `test -S "` + daemonSSHProxySock + `"`}
	_, err := runtime.ExecInContainerWithEnv(e.containerRuntime, daemonName, cmdArgs, nil, execUser)
	return err
}

func (e *RuntimeEngine) promptForAttachOrRestart(_ string) (string, error) {
	if ui.GumAvailable() {
		cmd := ui.GetGumCommand("choose",
			"Attach to existing session (recommended)",
			"Stop and create new session",
			"Abort")
		output, err := cmd.Output()
		if err != nil {
			return "", err
		}

		selected := strings.TrimSpace(string(output))
		switch {
		case strings.HasPrefix(selected, "Attach"):
			return "attach", nil
		case strings.HasPrefix(selected, "Stop"):
			return "restart", nil
		default:
			return "abort", fmt.Errorf("aborted")
		}
	}

	// Fallback
	fmt.Println("1. Attach to existing session (recommended)")
	fmt.Println("2. Stop and create new session")
	fmt.Println("3. Abort")
	fmt.Print("Choice [1-3]: ")
	var basicChoice string
	_, _ = fmt.Scanln(&basicChoice) //nolint:errcheck
	switch basicChoice {
	case "1":
		return "attach", nil
	case "2":
		return "restart", nil
	default:
		return "abort", fmt.Errorf("aborted")
	}
}

func (e *RuntimeEngine) ensureImageExists() {
	checkCmdArgs := runtime.GetCheckImageCommand(e.containerRuntime)
	checkCmd := exec.Command(checkCmdArgs[0], checkCmdArgs[1:]...)
	checkCmd.Dir = config.GetContainerDir()
	if err := checkCmd.Run(); err != nil {
		fmt.Println("Construct image not found. Building...")
		runtime.BuildImage(e.cfg)
		fmt.Println()
	}
}

func (e *RuntimeEngine) warnDaemonMountFallback() {
	if ui.CurrentLogLevel < ui.LogLevelInfo {
		return
	}
	fmt.Println("Daemon workspace does not include the current directory; running without daemon.")
	fmt.Println("Tip: Enable multi-root daemon mounts in config for always-fast starts.")
}

func buildDaemonExecEnv(args []string, providerEnv []string, cbServer *clipboard.Server, cfg *config.Config) []string {
	envVars := providerEnv

	if cbServer != nil {
		env.SetEnvVar(&envVars, "CONSTRUCT_CLIPBOARD_URL", cbServer.URL)
		env.SetEnvVar(&envVars, "CONSTRUCT_CLIPBOARD_TOKEN", cbServer.Token)
		env.SetEnvVar(&envVars, "CONSTRUCT_FILE_PASTE_AGENTS", constants.FileBasedPasteAgents)
	}

	clipboardPatchValue := "1"
	if cfg != nil && !cfg.Agents.ClipboardImagePatch {
		clipboardPatchValue = "0"
	}
	env.SetEnvVar(&envVars, "CONSTRUCT_CLIPBOARD_IMAGE_PATCH", clipboardPatchValue)

	if clipboardPatchValue != "0" {
		display := ":0"
		if d := os.Getenv("CONSTRUCT_X11_DISPLAY"); d != "" {
			display = d
		}
		env.SetEnvVar(&envVars, "DISPLAY", display)
	}

	if len(args) > 0 {
		env.SetEnvVar(&envVars, "CONSTRUCT_AGENT_NAME", args[0])
		appendAgentSpecificExecEnv(&envVars, args[0], clipboardPatchValue)
	}

	if os.Getenv("CONSTRUCT_DEBUG") == "1" {
		env.SetEnvVar(&envVars, "CONSTRUCT_DEBUG", "1")
	}

	if colorterm := os.Getenv("COLORTERM"); colorterm != "" {
		env.SetEnvVar(&envVars, "COLORTERM", colorterm)
	} else {
		env.SetEnvVar(&envVars, "COLORTERM", "truecolor")
	}

	if stdruntime.GOOS == "linux" {
		env.SetEnvVar(&envVars, "SSH_AUTH_SOCK", "/ssh-agent")
	}

	return envVars
}

func (e *RuntimeEngine) runAgentPatchInDaemon(daemonName, execUser string) error {
	patchScript := "/home/construct/.config/construct-cli/container/agent-patch.sh"
	args := []string{"bash", patchScript}

	patchEnv := make([]string, 0, 3)
	patchEnv = append(patchEnv, "CONSTRUCT_DEBUG=0")
	display := ":0"
	if d := os.Getenv("CONSTRUCT_X11_DISPLAY"); d != "" {
		display = d
	}
	patchEnv = append(patchEnv, "CONSTRUCT_X11_DISPLAY="+display)

	clipboardPatchValue := "1"
	if e.cfg != nil && !e.cfg.Agents.ClipboardImagePatch {
		clipboardPatchValue = "0"
	}
	patchEnv = append(patchEnv, "CONSTRUCT_CLIPBOARD_IMAGE_PATCH="+clipboardPatchValue)

	// Run non-interactively and silently
	_, err := execInteractiveAsUserFn(e.containerRuntime, daemonName, args, patchEnv, "", execUser)
	return err
}

func appendAgentSpecificRunFlags(runFlags *[]string, agentName, clipboardPatchValue string) {
	switch agentName {
	case "codex":
		*runFlags = append(*runFlags, "-e", "CODEX_HOME=/home/construct/.codex")
	case "gemini", "pi", "claude", "copilot":
		if clipboardPatchValue != "0" {
			*runFlags = append(*runFlags, "-e", "XDG_SESSION_TYPE=wayland")
		}
	}
}

func appendAgentSpecificExecEnv(envVars *[]string, agentName, clipboardPatchValue string) {
	switch agentName {
	case "codex":
		env.SetEnvVar(envVars, "CODEX_HOME", "/home/construct/.codex")
	case "gemini", "pi", "claude", "copilot":
		if clipboardPatchValue != "0" {
			env.SetEnvVar(envVars, "XDG_SESSION_TYPE", "wayland")
		}
	}
}

func appendExecUserRunFlags(runFlags *[]string, cfg *config.Config, containerRuntime string) {
	if containerRuntime != "docker" || stdruntime.GOOS != "linux" {
		return
	}
	if cfg == nil || !cfg.Sandbox.ExecAsHostUser {
		return
	}

	uid := os.Getuid()
	gid := os.Getgid()
	if uid == 0 {
		return
	}

	*runFlags = append(*runFlags, "--user", fmt.Sprintf("%d:%d", uid, gid))
	*runFlags = append(*runFlags, "-e", "HOME=/home/construct")
}

func collectForwardedEnv(cfg *config.Config, providerEnv []string) []string {
	// Start with standard provider keys from host
	hostEnvs := env.CollectProviderEnv()

	// Merge with providerEnv (providerEnv wins)
	merged := env.MergeEnvVars(hostEnvs, providerEnv)

	seen := make(map[string]struct{})
	for _, e := range merged {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			seen[parts[0]] = struct{}{}
		}
	}

	if cfg != nil {
		for _, e := range cfg.Sandbox.EnvPassthrough {
			if _, exists := seen[e]; !exists {
				if val := os.Getenv(e); val != "" {
					merged = append(merged, e+"="+val)
					seen[e] = struct{}{}
				}
			}
		}
	}

	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "CNSTR_") {
			parts := strings.SplitN(e, "=", 2)
			if len(parts) == 2 {
				key := strings.TrimPrefix(parts[0], "CNSTR_")
				if _, exists := seen[key]; !exists {
					merged = append(merged, key+"="+parts[1])
					seen[key] = struct{}{}
				}
			}
		}
	}

	return merged
}

func applyYoloArgs(args []string, cfg *config.Config) []string {
	if cfg == nil || len(args) == 0 {
		return args
	}

	agent := strings.ToLower(args[0])
	flag, ok := yoloFlagForAgent(agent)
	if !ok {
		return args
	}
	if !shouldEnableYolo(agent, cfg) {
		return args
	}
	if slices.Contains(args, flag) {
		return args
	}

	updated := make([]string, 0, len(args)+1)
	updated = append(updated, args[0], flag)
	updated = append(updated, args[1:]...)
	return updated
}

func yoloFlagForAgent(agent string) (string, bool) {
	switch agent {
	case "claude":
		return "--dangerously-skip-permissions", true
	case "copilot":
		return "--allow-all-tools", true
	case "gemini", "codex", "qwen", "cline", "kilocode", "crush":
		return "--yolo", true
	default:
		return "", false
	}
}

func shouldEnableYolo(agent string, cfg *config.Config) bool {
	if cfg.Agents.YoloAll {
		return true
	}
	for _, name := range cfg.Agents.YoloAgents {
		if strings.EqualFold(name, agent) || strings.EqualFold(name, "all") {
			return true
		}
	}
	return false
}

func resolveExecUserForRunningContainer(cfg *config.Config, containerRuntime, _ string) string {
	if containerRuntime != "docker" || stdruntime.GOOS != "linux" {
		// Daemon runs as root (USER construct is commented out in Dockerfile for
		// entrypoint permission-fixing). Without an explicit user, docker exec inherits
		// root — which causes agents like Claude to reject flags like
		// --dangerously-skip-permissions. Default to "construct" on non-Linux.
		return "construct"
	}
	if cfg == nil || !cfg.Sandbox.ExecAsHostUser {
		return "construct"
	}
	if runtime.UsesUserNamespaceRemap(containerRuntime) {
		return "construct"
	}

	uid := os.Getuid()
	if uid == 0 {
		return "construct"
	}

	return fmt.Sprintf("%d:%d", uid, os.Getgid())
}

func shouldEnableLoginForward(args []string) (bool, []int) {
	if ports, ok := readLoginBridgePorts(); ok {
		return true, ports
	}
	for _, arg := range args {
		if arg == "login" || arg == "auth" {
			ports := parsePorts(defaultLoginForwardPorts)
			if len(ports) == 0 {
				return true, []int{1455}
			}
			return true, ports
		}
	}
	return false, nil
}

func readLoginBridgePorts() ([]int, bool) {
	path := filepath.Join(config.GetConfigDir(), loginBridgeFlagFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return parsePorts(string(data)), true
}

func parsePorts(s string) []int {
	parts := strings.Split(s, ",")
	ports := make([]int, 0)
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		var port int
		fmt.Sscanf(p, "%d", &port) //nolint:errcheck
		if port > 0 {
			ports = append(ports, port)
		}
	}
	return ports
}

func formatPorts(ports []int) string {
	parts := make([]string, 0, len(ports))
	for _, p := range ports {
		parts = append(parts, fmt.Sprintf("%d", p))
	}
	return strings.Join(parts, ",")
}

func applyConstructPath(osEnv *[]string) {
	// Strict replacement for compatibility
	constructPath := env.BuildConstructPath("/home/construct")
	env.SetEnvVar(osEnv, "PATH", constructPath)
	env.SetEnvVar(osEnv, "CONSTRUCT_PATH", constructPath)
}

func mapDaemonWorkdir(cwd, mountSource, mountDest string) (string, bool) {
	rel, err := filepath.Rel(filepath.Clean(mountSource), filepath.Clean(cwd))
	if err != nil {
		return "", false
	}
	if rel == "." {
		return mountDest, true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false
	}
	return filepath.Join(mountDest, rel), true
}

// Compatibility wrappers for existing unit tests
func ensureAgentRuntimeDirs(args []string, configPath string) error {
	e := &RuntimeEngine{args: args, configPath: configPath}
	return e.ensureAgentRuntimeDirs()
}

func appendAgentSpecificDaemonEnv(envVars *[]string, agentName string) {
	appendAgentSpecificExecEnv(envVars, agentName, "1")
}

func execInRunningContainer(args []string, cfg *config.Config, containerRuntime string, providerEnv []string) (int, error) {
	e := NewRuntimeEngine(cfg, args, containerRuntime, "", providerEnv)
	// Manual setup since we're bypassing Prepare()
	e.sec = security.NewNoOpSession("")

	// Initialize clipboard for tests
	clipboardHost := ""
	if cfg != nil {
		clipboardHost = cfg.Sandbox.ClipboardHost
	}
	cbServer, _ := startClipboardServerFn(clipboardHost) //nolint:errcheck
	e.cbServer = cbServer

	return e.execInRunningContainer(args, "construct-cli", providerEnv)
}

func buildRunFlags(args []string, cfg *config.Config, containerRuntime string, osEnv []string, cbServer *clipboard.Server, mergedProviderEnv []string, loginForward bool, loginPorts []int) []string {
	e := &RuntimeEngine{
		cfg:              cfg,
		args:             args,
		containerRuntime: containerRuntime,
		osEnv:            osEnv,
		cbServer:         cbServer,
		loginForward:     loginForward,
		loginPorts:       loginPorts,
	}
	e.sec = security.NewNoOpSession("")
	var runFlags []string
	e.buildRunFlags(&runFlags, mergedProviderEnv)
	return runFlags
}

// execUserForAgentExec was removed: the docker-run path uses buildRunFlags which
// already sets --user construct on macOS, and appendExecUserRunFlags for Linux host-uid.

func startDaemonSSHBridge(cfg *config.Config, containerRuntime, daemonName, execUser string) (*SSHBridge, []string, error) {
	if stdruntime.GOOS != "darwin" {
		return nil, nil, nil
	}
	if cfg == nil || !cfg.Sandbox.ForwardSSHAgent {
		return nil, nil, nil
	}
	if os.Getenv("SSH_AUTH_SOCK") == "" {
		return nil, nil, nil
	}

	bridge, err := StartSSHBridge()
	if err != nil {
		return nil, nil, err
	}

	if err := ensureDaemonSSHProxy(containerRuntime, daemonName, bridge.Port, execUser); err != nil {
		bridge.Stop()
		return nil, nil, err
	}
	if err := waitForDaemonSSHProxy(containerRuntime, daemonName, execUser); err != nil {
		bridge.Stop()
		return nil, nil, err
	}

	envVars := []string{
		fmt.Sprintf("CONSTRUCT_SSH_BRIDGE_PORT=%d", bridge.Port),
		"SSH_AUTH_SOCK=" + daemonSSHProxySock,
	}
	return bridge, envVars, nil
}

func ensureDaemonSSHProxy(containerRuntime, daemonName string, port int, execUser string) error {
	envVars := []string{fmt.Sprintf("CONSTRUCT_SSH_BRIDGE_PORT=%d", port)}
	cmdArgs := []string{"bash", "-lc", `if ! command -v socat >/dev/null; then echo "socat not found" >&2; exit 1; fi; PROXY_SOCK="` + daemonSSHProxySock + `"; PROXY_DIR="$(dirname "$PROXY_SOCK")"; mkdir -p "$PROXY_DIR" 2>/dev/null || true; chmod 700 "$PROXY_DIR" 2>/dev/null || true; rm -f "$PROXY_SOCK"; nohup socat UNIX-LISTEN:"$PROXY_SOCK",fork,mode=600 TCP:host.docker.internal:"$CONSTRUCT_SSH_BRIDGE_PORT" >/tmp/socat.log 2>&1 &`}
	_, err := runtime.ExecInContainerWithEnv(containerRuntime, daemonName, cmdArgs, envVars, execUser)
	return err
}

func waitForDaemonSSHProxy(containerRuntime, daemonName, execUser string) error {
	for i := 0; i < 10; i++ {
		cmdArgs := []string{"bash", "-lc", `test -S "` + daemonSSHProxySock + `"`}
		if _, err := runtime.ExecInContainerWithEnv(containerRuntime, daemonName, cmdArgs, nil, execUser); err == nil {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("SSH agent proxy not ready")
}
