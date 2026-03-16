package sys

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/EstebanForge/construct-cli/internal/config"
	"github.com/EstebanForge/construct-cli/internal/runtime"
)

// ClipboardDebug prints host and container clipboard bridge diagnostics.
func ClipboardDebug(cfg *config.Config) {
	if cfg == nil {
		var err error
		cfg, _, err = config.Load()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to load config: %v\n", err)
			os.Exit(1)
		}
	}

	hostLogPath := filepath.Join(config.GetLogsDir(), "debug_clipboard_server.log")
	fmt.Printf("Host clipboard server log: %s\n", hostLogPath)
	if _, err := os.Stat(hostLogPath); err == nil {
		if err := printTail(hostLogPath, 40); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to read host clipboard log: %v\n", err)
		}
	} else {
		fmt.Println("(missing)")
	}

	fmt.Println()
	fmt.Println("Container clipboard bridge status:")

	containerRuntime := runtime.DetectRuntime(cfg.Runtime.Engine)
	configPath := config.GetConfigDir()
	cmd, err := runtime.BuildComposeCommand(containerRuntime, configPath, "exec", []string{
		"-T",
		"construct-box",
		"bash",
		"-lc",
		`set -u
log_file="${CONSTRUCT_COPILOT_CLIPBOARD_LOG:-/tmp/construct-copilot-clipboard.log}"
echo "Patch targets:"
matches=$(find -L "$HOME/.npm-global" -type f -path "*/@teddyzhu/clipboard/index.js" 2>/dev/null || true)
if [[ -z "$matches" ]]; then
  echo "(no @teddyzhu/clipboard install found)"
else
  while IFS= read -r file; do
    [[ -z "$file" ]] && continue
    echo "- $file"
    if grep -q "construct-copilot-clipboard-bridge-v2" "$file"; then
      echo "  patched: yes"
    else
      echo "  patched: no"
    fi
  done <<< "$matches"
fi
echo
echo "Clipboard bridge log: $log_file"
if [[ -f "$log_file" ]]; then
  tail -n 80 "$log_file"
else
  echo "(missing)"
fi
echo
echo "Clipboard temp files:"
ls -l /tmp/construct-copilot-*.png /tmp/construct-clipboard.png 2>/dev/null || echo "(none)"
echo
echo "Clipboard sync process:"
ps -ef | grep -E "clipboard-x11-sync|Xvfb" | grep -v grep || echo "(not running)"
`,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to build clipboard debug command: %v\n", err)
		os.Exit(1)
	}

	cmd.Dir = config.GetContainerDir()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "\nClipboard debug failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "Start the Construct daemon first with 'construct sys shell' or run an agent session.")
		os.Exit(1)
	}
}

func printTail(path string, lines int) error {
	cmd := exec.Command("tail", "-n", fmt.Sprintf("%d", lines), path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
