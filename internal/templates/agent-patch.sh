#!/usr/bin/env bash
set -e

dbg() {
    if [[ "${CONSTRUCT_DEBUG:-0}" == "1" ]]; then
        echo "[agent-patch] $*" >&2
    fi
}

fix_clipboard_libs() {
    # Strategy: Find the directory where clipboardy expects xsel to be.
    # The path usually ends in .../clipboardy/fallbacks/linux
    # We search both linuxbrew (for gemini-cli) and npm-global (for qwen, etc.)

    # 1. Standard clipboardy structure
    find -L /home/linuxbrew/.linuxbrew "$HOME/.npm-global" -type d -path "*/clipboardy/fallbacks/linux" 2>/dev/null | while read -r dir; do
        # Shim xsel
        local xsel_bin="$dir/xsel"
        if [ -L "$xsel_bin" ] && [[ "$(readlink "$xsel_bin")" == *"/clipper"* ]]; then
             : # Already shimmed
        else
             rm -f "$xsel_bin" 2>/dev/null
             ln -sf /usr/local/bin/clipper "$xsel_bin"
        fi

        # Shim xclip
        local xclip_bin="$dir/xclip"
        rm -f "$xclip_bin" 2>/dev/null
        ln -sf /usr/local/bin/clipper "$xclip_bin"
    done

    # 2. Aggressive search for ANY rogue xsel in npm-global (for Qwen or others with weird structures)
    find -L "$HOME/.npm-global" -name "xsel" -type f 2>/dev/null | while read -r binary; do
        # Ignore if it's already our shim (which is a symlink, but -type f might catch it if following links? no, find -L does)
        # Actually -type f with -L matches symlinks to files.
        if [[ "$(readlink -f "$binary")" == *"/clipper"* ]]; then
            continue
        fi

        rm -f "$binary"
        ln -sf /usr/local/bin/clipper "$binary"
    done
}

patch_agent_code() {
    # Find all JS files that might contain the platform check
    # We look for files containing 'process.platform' and 'darwin'
    find -L /home/linuxbrew/.linuxbrew "$HOME/.npm-global" -type f -name "*.js" 2>/dev/null | xargs grep -l "process.platform" 2>/dev/null | xargs grep -l "darwin" 2>/dev/null | while read -r js_file; do
        if grep -q "process.platform !== \"darwin\"" "$js_file"; then
            # Replace platform check with a dummy 'false' to allow the code to run on Linux
            sed -i 's/process.platform !== \"darwin\"/false/g' "$js_file"
        elif grep -q "process.platform !== 'darwin'" "$js_file"; then
            sed -i "s/process.platform !== 'darwin'/false/g" "$js_file"
        fi
    done
}

patch_copilot_clipboard() {
    # Search in npm-global, Homebrew, and GitHub CLI extensions
    find -L "$HOME/.npm-global" /home/linuxbrew/.linuxbrew "$HOME/.local/share/gh/extensions" -type f -path "*/@teddyzhu/clipboard/index.js" 2>/dev/null | while read -r js_file; do
        if grep -q "construct-copilot-clipboard-bridge-v2" "$js_file"; then
            dbg "Copilot clipboard bridge already patched: $js_file"
            continue
        fi

        dbg "Patching Copilot clipboard bridge: $js_file"
        cat >> "$js_file" <<'EOF'

// construct-copilot-clipboard-bridge-v2
try {
  const fs = require('fs')
  const os = require('os')
  const path = require('path')
  const { execFileSync } = require('child_process')

  const hostUrl = process.env.CONSTRUCT_CLIPBOARD_URL
  const authToken = process.env.CONSTRUCT_CLIPBOARD_TOKEN
  const debugEnabled = process.env.CONSTRUCT_DEBUG === '1'
  const debugLogPath = process.env.CONSTRUCT_COPILOT_CLIPBOARD_LOG || '/tmp/construct-copilot-clipboard.log'

  const debugLog = (message) => {
    if (!debugEnabled) {
      return
    }

    try {
      fs.appendFileSync(
        debugLogPath,
        `[${new Date().toISOString()}] ${message}\n`
      )
    } catch {}
  }

  debugLog(
    `bridge loaded pid=${process.pid} host_url=${hostUrl ? 'set' : 'missing'} token=${authToken ? 'set' : 'missing'}`
  )

  const fetchClipboardImage = () => {
    if (!hostUrl || !authToken) {
      debugLog('fetch skipped: missing host url or token')
      return null
    }

    try {
      debugLog('fetch start: requesting image/png from host bridge')
      const data = execFileSync(
        'curl',
        [
          '-sSf',
          '-H',
          `X-Construct-Clip-Token: ${authToken}`,
          `${hostUrl}/paste?type=image/png`,
        ],
        { encoding: 'buffer', stdio: ['ignore', 'pipe', 'ignore'] }
      )

      if (!data || data.length === 0) {
        debugLog('fetch result: empty response')
        return null
      }

      debugLog(`fetch result: ${data.length} bytes`)
      return Buffer.from(data)
    } catch (error) {
      debugLog(`fetch failed: ${error && error.message ? error.message : 'unknown error'}`)
      return null
    }
  }

  const writeClipboardImageFile = (buffer) => {
    if (!buffer || buffer.length === 0) {
      return null
    }

    const filePath = path.join(
      os.tmpdir(),
      `construct-copilot-${process.pid}-${Date.now()}.png`
    )
    fs.writeFileSync(filePath, buffer)
    debugLog(`temp file written: ${filePath} (${buffer.length} bytes)`)
    return filePath
  }

  const originalGetFiles = module.exports.ClipboardManager?.prototype?.getFiles
  if (typeof originalGetFiles === 'function') {
    module.exports.ClipboardManager.prototype.getFiles = function (...args) {
      debugLog('ClipboardManager.getFiles called')
      const buffer = fetchClipboardImage()
      if (buffer) {
        const filePath = writeClipboardImageFile(buffer)
        if (filePath) {
          debugLog(`ClipboardManager.getFiles returning bridged file: ${filePath}`)
          return [filePath]
        }
      }

      debugLog('ClipboardManager.getFiles falling back to original implementation')
      return originalGetFiles.apply(this, args)
    }
  }

  const originalGetImageData = module.exports.ClipboardManager?.prototype?.getImageData
  if (typeof originalGetImageData === 'function') {
    module.exports.ClipboardManager.prototype.getImageData = function (...args) {
      debugLog('ClipboardManager.getImageData called')
      const buffer = fetchClipboardImage()
      if (buffer) {
        debugLog(`ClipboardManager.getImageData returning bridged buffer: ${buffer.length} bytes`)
        return {
          data: buffer,
          size: buffer.length,
        }
      }

      debugLog('ClipboardManager.getImageData falling back to original implementation')
      return originalGetImageData.apply(this, args)
    }
  }

  const originalGetClipboardFiles = module.exports.getClipboardFiles
  if (typeof originalGetClipboardFiles === 'function') {
    module.exports.getClipboardFiles = function (...args) {
      debugLog('getClipboardFiles called')
      const buffer = fetchClipboardImage()
      if (buffer) {
        const filePath = writeClipboardImageFile(buffer)
        if (filePath) {
          debugLog(`getClipboardFiles returning bridged file: ${filePath}`)
          return [filePath]
        }
      }

      debugLog('getClipboardFiles falling back to original implementation')
      return originalGetClipboardFiles.apply(this, args)
    }
  }

  const originalGetClipboardImageData = module.exports.getClipboardImageData
  if (typeof originalGetClipboardImageData === 'function') {
    module.exports.getClipboardImageData = function (...args) {
      debugLog('getClipboardImageData called')
      const buffer = fetchClipboardImage()
      if (buffer) {
        debugLog(`getClipboardImageData returning bridged buffer: ${buffer.length} bytes`)
        return {
          data: buffer,
          size: buffer.length,
        }
      }

      debugLog('getClipboardImageData falling back to original implementation')
      return originalGetClipboardImageData.apply(this, args)
    }
  }
} catch (error) {
  try {
    require('fs').appendFileSync(
      process.env.CONSTRUCT_COPILOT_CLIPBOARD_LOG || '/tmp/construct-copilot-clipboard.log',
      `[${new Date().toISOString()}] bridge setup failed: ${error && error.message ? error.message : 'unknown error'}\n`
    )
  } catch {}
}
// /construct-copilot-clipboard-bridge-v2
EOF
    done
}

echo "🔧 Patching clipboard support for agents..."
fix_clipboard_libs
echo "🔧 Patching Copilot clipboard bridge..."
patch_copilot_clipboard
echo "🔧 Patching agent code for cross-platform clipboard..."
patch_agent_code
