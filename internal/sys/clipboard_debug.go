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

	// Host-side server log (always-on since this release).
	hostLogPath := filepath.Join(config.GetLogsDir(), "clipboard_server.log")
	fmt.Printf("=== Host clipboard server log: %s ===\n", hostLogPath)
	if _, err := os.Stat(hostLogPath); err == nil {
		if err := printTail(hostLogPath, 50); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to read host clipboard log: %v\n", err)
		}
	} else {
		fmt.Println("(missing — start an agent session to populate)")
	}

	fmt.Println()
	fmt.Println("=== Container clipboard diagnostics ===")

	containerRuntime := runtime.DetectRuntime(cfg.Runtime.Engine)
	configPath := config.GetConfigDir()
	cmd, err := runtime.BuildComposeCommand(containerRuntime, configPath, "exec", []string{
		"-T",
		"construct-box",
		"bash",
		"-lc",
		`set -u

echo "--- Clipboard environment ---"
for var in CONSTRUCT_AGENT_NAME CONSTRUCT_CLIPBOARD_URL CONSTRUCT_CLIPBOARD_TOKEN \
           CONSTRUCT_FILE_PASTE_AGENTS CONSTRUCT_CLIPBOARD_IMAGE_PATCH \
           XDG_SESSION_TYPE WAYLAND_DISPLAY DISPLAY; do
  val="${!var:-<unset>}"
  # Truncate token for safety
  if [[ "$var" == "CONSTRUCT_CLIPBOARD_TOKEN" ]] && [[ "$val" != "<unset>" ]]; then
    val="${val:0:8}..."
  fi
  echo "  $var=$val"
done

echo
echo "--- Clipper shim ---"
for bin in /usr/bin/wl-paste /usr/bin/xclip /usr/bin/xsel /usr/local/bin/clipper; do
  if [[ -e "$bin" ]]; then
    if [[ -L "$bin" ]]; then
      echo "  $bin -> $(readlink "$bin")"
    else
      echo "  $bin (regular file)"
    fi
  else
    echo "  $bin (missing)"
  fi
done

echo
echo "--- Clipper log (last 40 lines): /tmp/construct-clipper.log ---"
if [[ -f /tmp/construct-clipper.log ]]; then
  tail -n 40 /tmp/construct-clipper.log
else
  echo "(missing — paste an image during an agent session to populate)"
fi

echo "--- Codex clipboard ---"
codex_bin=$(command -v codex 2>/dev/null || true)
echo "  which codex resolves to: ${codex_bin:-(not found)}"
if [[ -n "$codex_bin" ]] && grep -q "construct-codex-wrapper-v1" "$codex_bin" 2>/dev/null; then
  echo "PTY wrapper v1: installed at $codex_bin"
  if [[ -x "$codex_bin" ]]; then
    echo "  executable: YES"
  else
    echo "  executable: NO (chmod failed?)"
  fi
  echo "  shebang: $(head -1 "$codex_bin")"
  real_line=$(grep "_REAL" "$codex_bin" 2>/dev/null | head -1)
  echo "  _REAL: $real_line"
else
  echo "PTY wrapper: NOT installed at active codex path (run 'construct sys rebuild' then restart agent)"
fi
codex_wrapper_log="$HOME/.config/construct-cli/logs/construct-codex-wrapper.log"
echo "Wrapper log: $codex_wrapper_log"
if [[ -f "$codex_wrapper_log" ]]; then
  tail -n 40 "$codex_wrapper_log"
else
  echo "(missing — start a Codex session; log is always-on and persists to host)"
fi

echo
echo "--- Copilot clipboard ---"
copilot_bin=$(command -v copilot 2>/dev/null || true)
echo "  which copilot resolves to: ${copilot_bin:-(not found)}"
if [[ -n "$copilot_bin" ]] && grep -q "construct-copilot-wrapper-v9" "$copilot_bin" 2>/dev/null; then
  echo "PTY wrapper v9: installed at $copilot_bin"
  if [[ -x "$copilot_bin" ]]; then
    echo "  executable: YES"
  else
    echo "  executable: NO (chmod failed?)"
  fi
  echo "  shebang: $(head -1 "$copilot_bin")"
  real_line=$(grep "_REAL" "$copilot_bin" 2>/dev/null | head -1)
  echo "  _REAL: $real_line"
else
  echo "PTY wrapper: NOT installed at active copilot path (run 'construct sys rebuild' then restart agent)"
fi
wrapper_log="$HOME/.config/construct-cli/logs/construct-copilot-wrapper.log"
echo "Wrapper log: $wrapper_log"
if [[ -f "$wrapper_log" ]]; then
  tail -n 40 "$wrapper_log"
else
  echo "(missing — start a Copilot session; log is always-on and persists to host)"
fi
echo
echo "--- Copilot JS bridge ---"
log_file="${CONSTRUCT_COPILOT_CLIPBOARD_LOG:-/tmp/construct-copilot-clipboard.log}"
matches=$(find -L "$HOME/.npm-global" -type f -path "*/@teddyzhu/clipboard/index.js" 2>/dev/null || true)
if [[ -z "$matches" ]]; then
  echo "(no @teddyzhu/clipboard install found)"
else
  while IFS= read -r file; do
    [[ -z "$file" ]] && continue
    echo "- $file"
    if grep -q "construct-copilot-clipboard-bridge-v3" "$file"; then
      echo "  patched: yes"
    else
      echo "  patched: NO (run 'construct sys rebuild' then restart agent)"
    fi
  done <<< "$matches"
fi
echo "JS bridge log: $log_file"
if [[ -f "$log_file" ]]; then
  tail -n 40 "$log_file"
else
  echo "(missing)"
fi

echo
echo "--- Clipboard temp files ---"
ls -l /tmp/construct-copilot-*.png /tmp/construct-clipboard*.png 2>/dev/null || echo "(none)"

echo
echo "--- Clipboard sync process ---"
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
