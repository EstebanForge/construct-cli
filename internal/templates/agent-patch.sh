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
        if grep -q "construct-copilot-clipboard-bridge-v3" "$js_file"; then
            dbg "Copilot clipboard bridge already patched: $js_file"
            continue
        fi

        local js_dir
        js_dir=$(dirname "$js_file")

        # @teddyzhu/clipboard is a NAPI-RS native addon. On Linux containers it has no
        # compiled .node binaries, so the original index.js throws before any appended
        # bridge code runs. We replace the file entirely with a pure-JS implementation
        # that exports the same ClipboardManager API via the Construct clipboard bridge.
        dbg "Replacing Copilot clipboard with pure-JS bridge: $js_file"
        cat > "$js_file" <<'EOF'
// construct-copilot-clipboard-bridge-v3
// Pure-JS replacement for @teddyzhu/clipboard — no native binary required.
// Fetches image/text from the Construct host clipboard bridge via HTTP.
'use strict'

const fs = require('fs')
const os = require('os')
const path = require('path')
const { execFileSync } = require('child_process')

const hostUrl = process.env.CONSTRUCT_CLIPBOARD_URL
const authToken = process.env.CONSTRUCT_CLIPBOARD_TOKEN
const debugEnabled = process.env.CONSTRUCT_DEBUG === '1'
const debugLogPath = process.env.CONSTRUCT_COPILOT_CLIPBOARD_LOG || '/tmp/construct-copilot-clipboard.log'

const debugLog = (message) => {
  if (!debugEnabled) return
  try {
    fs.appendFileSync(debugLogPath, `[${new Date().toISOString()}] ${message}\n`)
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
      ['-sSf', '-H', `X-Construct-Clip-Token: ${authToken}`, `${hostUrl}/paste?type=image/png`],
      { encoding: 'buffer', stdio: ['ignore', 'pipe', 'ignore'] }
    )
    if (!data || data.length === 0) { debugLog('fetch result: empty response'); return null }
    debugLog(`fetch result: ${data.length} bytes`)
    return Buffer.from(data)
  } catch (err) {
    debugLog(`fetch failed: ${err && err.message ? err.message : 'unknown error'}`)
    return null
  }
}

const fetchClipboardText = () => {
  if (!hostUrl || !authToken) return ''
  try {
    const data = execFileSync(
      'curl',
      ['-sSf', '-H', `X-Construct-Clip-Token: ${authToken}`, `${hostUrl}/paste?type=text/plain`],
      { encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }
    )
    return data || ''
  } catch { return '' }
}

const writeTempFile = (buffer) => {
  const filePath = path.join(os.tmpdir(), `construct-copilot-${process.pid}-${Date.now()}.png`)
  fs.writeFileSync(filePath, buffer)
  debugLog(`temp file written: ${filePath} (${buffer.length} bytes)`)
  return filePath
}

class ClipboardManager {
  getImageData() {
    debugLog('ClipboardManager.getImageData called')
    const buffer = fetchClipboardImage()
    if (!buffer) { debugLog('ClipboardManager.getImageData: no image'); return { data: null, size: 0 } }
    debugLog(`ClipboardManager.getImageData returning ${buffer.length} bytes`)
    return { data: buffer, size: buffer.length }
  }
  getFiles() {
    debugLog('ClipboardManager.getFiles called')
    const buffer = fetchClipboardImage()
    if (!buffer) { debugLog('ClipboardManager.getFiles: no image'); return [] }
    const filePath = writeTempFile(buffer)
    debugLog(`ClipboardManager.getFiles returning: ${filePath}`)
    return [filePath]
  }
}

module.exports = {
  ClipboardManager,
  getClipboardText: () => { debugLog('getClipboardText called'); return fetchClipboardText() },
  getClipboardImageData: () => {
    debugLog('getClipboardImageData called')
    const buffer = fetchClipboardImage()
    if (!buffer) return null
    return { data: buffer, size: buffer.length }
  },
  getClipboardFiles: () => {
    debugLog('getClipboardFiles called')
    const buffer = fetchClipboardImage()
    if (!buffer) return []
    return [writeTempFile(buffer)]
  },
  getClipboardImage: () => fetchClipboardImage(),
  getClipboardBuffer: () => fetchClipboardImage(),
  getClipboardHtml: () => '',
  getClipboardImageRaw: () => null,
  getFullClipboardData: () => null,
  isWaylandClipboardAvailable: () => false,
  setClipboardText: () => {},
  setClipboardImage: () => {},
  setClipboardContents: () => {},
  setClipboardFiles: () => {},
  setClipboardBuffer: () => {},
  setClipboardHtml: () => {},
  setClipboardImageRaw: () => {},
  clearClipboard: () => {},
  ClipboardListener: class ClipboardListener { on() {} off() {} start() {} stop() {} },
}
// /construct-copilot-clipboard-bridge-v3
EOF
    done
}

patch_copilot_keybinding() {
    # Copilot's image paste handler uses `fe.meta` on non-darwin (Linux), meaning
    # it requires Meta/Alt+V instead of Ctrl+V for image paste inside the container.
    # Patch app.js to also accept fe.ctrl on Linux so Ctrl+V works as expected.
    local app_js="$HOME/.npm-global/lib/node_modules/@github/copilot/app.js"
    if [[ ! -f "$app_js" ]]; then
        dbg "Copilot app.js not found, skipping keybinding patch"
        return
    fi

    if grep -q "construct-copilot-keybinding-v1" "$app_js"; then
        dbg "Copilot keybinding already patched"
        return
    fi

    # Replace: (CLi()!=="darwin"?fe.meta:fe.ctrl)&&fe.name==="v"
    # With:    (CLi()!=="darwin"?fe.ctrl||fe.meta:fe.ctrl)&&fe.name==="v"
    if grep -qF '(CLi()!=="darwin"?fe.meta:fe.ctrl)&&fe.name==="v"' "$app_js"; then
        sed -i 's/(CLi()!=="darwin"?fe\.meta:fe\.ctrl)&&fe\.name==="v"/(CLi()!=="darwin"?fe.ctrl||fe.meta:fe.ctrl)\&\&fe.name==="v"/g' "$app_js"
        echo "// construct-copilot-keybinding-v1" >> "$app_js"
        dbg "Copilot keybinding patched: Ctrl+V now triggers image paste on Linux"
    else
        dbg "Copilot keybinding pattern not found in app.js (version mismatch?)"
    fi
}

patch_copilot_paste_wrapper() {
    local active_copilot
    active_copilot=$(command -v copilot 2>/dev/null || true)

    if [[ -z "$active_copilot" ]]; then
        dbg "copilot not found in PATH — skipping wrapper install"
        return
    fi

    local real_target
    real_target=$(find_real_npm_binary "copilot" "@github/copilot-cli")
    if [[ -z "$real_target" ]]; then
        dbg "Cannot find real copilot binary in npm-global — skipping wrapper install"
        return
    fi
    dbg "Real copilot target: $real_target"

    # Choose wrapper install location. Never overwrite the real npm-global copilot binary.
    local wrapper_path="$active_copilot"
    if [[ "$wrapper_path" == "$real_target" ]]; then
        if [[ -d /home/linuxbrew/.linuxbrew/bin ]] && [[ -w /home/linuxbrew/.linuxbrew/bin ]]; then
            wrapper_path="/home/linuxbrew/.linuxbrew/bin/copilot"
        else
            wrapper_path="$HOME/.local/bin/copilot"
            mkdir -p "$HOME/.local/bin"
        fi
    fi

    # Skip if our wrapper is already in place (idempotent guard on version string).
    if grep -q "construct-copilot-wrapper-v10" "$wrapper_path" 2>/dev/null; then
        dbg "Copilot PTY wrapper v10 already at $wrapper_path"
        return
    fi

    # Remove any stale copilot-real created by previous wrapper versions.
    rm -f "${wrapper_path}-real"
    # Remove prior wrapper path before writing the new wrapper script.
    rm -f "$wrapper_path"

    dbg "Installing Copilot clipboard PTY wrapper at $wrapper_path (real: $real_target)"
    cat > "$wrapper_path" << 'PYEOF'
#!/home/linuxbrew/.linuxbrew/bin/python3
# construct-copilot-wrapper-v10
# PTY interceptor: catches Ctrl+V, saves clipboard image to .construct-clipboard/,
# and injects the file path as text into Copilot's input.
import fcntl, os, pty, select, signal, struct, subprocess, sys, termios, time, tty

_URL   = os.environ.get('CONSTRUCT_CLIPBOARD_URL', '')
_TOKEN = os.environ.get('CONSTRUCT_CLIPBOARD_TOKEN', '')
_DIR   = '.construct-clipboard'
_LOGDIR = os.path.expanduser('~/.config/construct-cli/logs')
_LOG    = os.path.join(_LOGDIR, 'construct-copilot-wrapper.log')
_REAL   = '__CONSTRUCT_REAL_COPILOT__'

def _log(msg):
    try:
        os.makedirs(_LOGDIR, exist_ok=True)
        with open(_LOG, 'a') as f:
            f.write(f'[{time.strftime("%H:%M:%S")}] {msg}\n')
    except Exception:
        pass

def _save_image():
    _log(f'paste detected: url_set={bool(_URL)} token_set={bool(_TOKEN)}')
    if not _URL or not _TOKEN:
        _log('save_image: bridge env not set — cannot fetch')
        return None
    try:
        os.makedirs(_DIR, exist_ok=True)
    except Exception:
        return None
    ts  = int(time.time() * 1000)
    img = os.path.abspath(f'{_DIR}/clipboard-{ts}.png')
    r = subprocess.run(
        ['curl', '-sSf', '-H', f'X-Construct-Clip-Token: {_TOKEN}',
         f'{_URL}/paste?type=image/png', '-o', img],
        capture_output=True, timeout=5,
    )
    if r.returncode != 0 or not os.path.exists(img) or os.path.getsize(img) == 0:
        try:
            os.remove(img)
        except Exception:
            pass
        _log(f'save_image: fetch failed rc={r.returncode} stderr={r.stderr[:200]}')
        return None
    _log(f'save_image: saved {img} ({os.path.getsize(img)} bytes)')
    return img

_PASTE_TRIGGERS = [b'\x16', b'\x1b[118;5u', b'\x1b[118;9u', b'\x1b[200~']

def _handle_paste(data):
    if not any(t in data for t in _PASTE_TRIGGERS):
        return data
    out = b''
    while data:
        earliest_idx = None
        earliest_seq = None
        for seq in _PASTE_TRIGGERS:
            idx = data.find(seq)
            if idx >= 0 and (earliest_idx is None or idx < earliest_idx):
                earliest_idx = idx
                earliest_seq = seq
        if earliest_idx is None:
            out += data
            break
        
        out += data[:earliest_idx]
        data = data[earliest_idx + len(earliest_seq):]
        
        img = _save_image()
        if img:
            if earliest_seq == b'\x1b[200~':
                end_idx = data.find(b'\x1b[201~')
                if end_idx >= 0:
                    data = data[end_idx + 6:]
            
            # Copilot usually wants @path for its CLI
            out += f'@{img} '.encode()
            _log(f'injected @{img}')
        else:
            _log('no image — forwarding raw trigger')
            out += earliest_seq
    return out

def _winsz(fd):
    try:
        buf = fcntl.ioctl(fd, termios.TIOCGWINSZ, b'\x00' * 8)
        return struct.unpack('HHHH', buf)[:2]
    except Exception:
        return (24, 80)

def _setwinsz(fd, rows, cols):
    try:
        fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack('HHHH', rows, cols, 0, 0))
    except Exception:
        pass

def main():
    real = _REAL
    if not os.path.isfile(real) or not os.access(real, os.X_OK):
        _log(f'ERROR: real binary not found or not executable: {real}')
        sys.exit(1)
    args = [real] + sys.argv[1:]

    fd_in = sys.stdin.fileno()
    try:
        old = termios.tcgetattr(fd_in)
    except termios.error:
        os.execv(real, args)
        return

    mfd, sfd = pty.openpty()
    r, c = _winsz(sys.stdout.fileno())
    _setwinsz(sfd, r, c)

    proc = subprocess.Popen(args, stdin=sfd, stdout=sfd, stderr=sfd,
                            close_fds=True, start_new_session=True)
    os.close(sfd)
    tty.setraw(fd_in)

    def _resize(sig, frame):
        r, c = _winsz(sys.stdout.fileno())
        _setwinsz(mfd, r, c)
    signal.signal(signal.SIGWINCH, _resize)

    out_fd = sys.stdout.fileno()
    try:
        while True:
            if proc.poll() is not None:
                break
            try:
                rl, _, _ = select.select([fd_in, mfd], [], [], 0.05)
            except (OSError, select.error):
                break

            if fd_in in rl:
                try:
                    data = os.read(fd_in, 4096)
                except OSError:
                    break
                if not data:
                    break
                data = _handle_paste(data)
                try:
                    os.write(mfd, data)
                except OSError:
                    break

            if mfd in rl:
                try:
                    data = os.read(mfd, 4096)
                    if data:
                        os.write(out_fd, data)
                except OSError:
                    break
    finally:
        try:
            termios.tcsetattr(fd_in, termios.TCSADRAIN, old)
        except Exception:
            pass
        try:
            os.close(mfd)
        except Exception:
            pass
    proc.wait()
    sys.exit(proc.returncode or 0)

if __name__ == '__main__':
    main()
PYEOF
    sed -i "s|__CONSTRUCT_REAL_COPILOT__|${real_target}|" "$wrapper_path"
    chmod +x "$wrapper_path"
    dbg "Copilot PTY wrapper v10 installed at $wrapper_path (real: $real_target)"
}

patch_codex_paste_wrapper() {
    local active_codex
    active_codex=$(command -v codex 2>/dev/null || true)

    if [[ -z "$active_codex" ]]; then
        dbg "codex not found in PATH — skipping wrapper install"
        return
    fi

    local real_target
    real_target=$(find_real_npm_binary "codex" "@openai/codex")
    if [[ -z "$real_target" ]]; then
        dbg "Cannot find real codex binary in npm-global — skipping wrapper install"
        return
    fi
    dbg "Real codex target: $real_target"

    local wrapper_path="$active_codex"
    if [[ "$wrapper_path" == "$real_target" ]]; then
        if [[ -d /home/linuxbrew/.linuxbrew/bin ]] && [[ -w /home/linuxbrew/.linuxbrew/bin ]]; then
            wrapper_path="/home/linuxbrew/.linuxbrew/bin/codex"
        else
            wrapper_path="$HOME/.local/bin/codex"
            mkdir -p "$HOME/.local/bin"
        fi
    fi

    if grep -q "construct-codex-wrapper-v2" "$wrapper_path" 2>/dev/null; then
        dbg "Codex PTY wrapper v2 already at $wrapper_path"
        return
    fi

    rm -f "${wrapper_path}-real"
    rm -f "$wrapper_path"

    dbg "Installing Codex clipboard PTY wrapper at $wrapper_path (real: $real_target)"
    cat > "$wrapper_path" << 'PYEOF'
#!/home/linuxbrew/.linuxbrew/bin/python3
# construct-codex-wrapper-v2
# PTY interceptor: catches Ctrl+V and injects image file path into Codex input.
import fcntl, os, pty, select, signal, struct, subprocess, sys, termios, time, tty

_URL   = os.environ.get('CONSTRUCT_CLIPBOARD_URL', '')
_TOKEN = os.environ.get('CONSTRUCT_CLIPBOARD_TOKEN', '')
_DIR   = '.construct-clipboard'
_LOGDIR = os.path.expanduser('~/.config/construct-cli/logs')
_LOG    = os.path.join(_LOGDIR, 'construct-codex-wrapper.log')
_REAL   = '__CONSTRUCT_REAL_CODEX__'

def _log(msg):
    try:
        os.makedirs(_LOGDIR, exist_ok=True)
        with open(_LOG, 'a') as f:
            f.write(f'[{time.strftime("%H:%M:%S")}] {msg}\n')
    except Exception:
        pass

def _save_image():
    _log(f'ctrl+v detected: url_set={bool(_URL)} token_set={bool(_TOKEN)}')
    if not _URL or not _TOKEN:
        _log('save_image: bridge env not set — cannot fetch')
        return None
    try:
        os.makedirs(_DIR, exist_ok=True)
    except Exception:
        return None
    ts  = int(time.time() * 1000)
    img = os.path.abspath(f'{_DIR}/clipboard-{ts}.png')
    r = subprocess.run(
        ['curl', '-sSf', '-H', f'X-Construct-Clip-Token: {_TOKEN}',
         f'{_URL}/paste?type=image/png', '-o', img],
        capture_output=True, timeout=5,
    )
    if r.returncode != 0 or not os.path.exists(img) or os.path.getsize(img) == 0:
        try:
            os.remove(img)
        except Exception:
            pass
        _log(f'save_image: fetch failed rc={r.returncode} stderr={r.stderr[:200]}')
        return None
    _log(f'save_image: saved {img} ({os.path.getsize(img)} bytes)')
    return img

_PASTE_TRIGGERS = [b'\x16', b'\x1b[118;5u', b'\x1b[118;9u', b'\x1b[200~']

def _handle_paste(data):
    if not any(t in data for t in _PASTE_TRIGGERS):
        return data
    out = b''
    while data:
        earliest_idx = None
        earliest_seq = None
        for seq in _PASTE_TRIGGERS:
            idx = data.find(seq)
            if idx >= 0 and (earliest_idx is None or idx < earliest_idx):
                earliest_idx = idx
                earliest_seq = seq
        if earliest_idx is None:
            out += data
            break
        
        out += data[:earliest_idx]
        data = data[earliest_idx + len(earliest_seq):]
        
        img = _save_image()
        if img:
            if earliest_seq == b'\x1b[200~':
                end_idx = data.find(b'\x1b[201~')
                if end_idx >= 0:
                    data = data[end_idx + 6:]
            
            # Codex wants raw path
            out += f'{img} '.encode()
            _log(f'injected {img}')
        else:
            _log('no image — forwarding raw trigger')
            out += earliest_seq
    return out

def _winsz(fd):
    try:
        buf = fcntl.ioctl(fd, termios.TIOCGWINSZ, b'\x00' * 8)
        return struct.unpack('HHHH', buf)[:2]
    except Exception:
        return (24, 80)

def _setwinsz(fd, rows, cols):
    try:
        fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack('HHHH', rows, cols, 0, 0))
    except Exception:
        pass

def main():
    real = _REAL
    if not os.path.isfile(real) or not os.access(real, os.X_OK):
        _log(f'ERROR: real binary not found or not executable: {real}')
        sys.exit(1)
    args = [real] + sys.argv[1:]
    _log(f'starting: real={real} url_set={bool(_URL)} token_set={bool(_TOKEN)} args={sys.argv[1:]}')

    fd_in = sys.stdin.fileno()
    try:
        old = termios.tcgetattr(fd_in)
    except termios.error:
        _log('not a tty — exec direct (no interception)')
        os.execv(real, args)
        return

    mfd, sfd = pty.openpty()
    r, c = _winsz(sys.stdout.fileno())
    _setwinsz(sfd, r, c)

    proc = subprocess.Popen(args, stdin=sfd, stdout=sfd, stderr=sfd,
                            close_fds=True, start_new_session=True)
    os.close(sfd)
    tty.setraw(fd_in)
    _log(f'pty ready: pid={proc.pid}')

    def _resize(sig, frame):
        r, c = _winsz(sys.stdout.fileno())
        _setwinsz(mfd, r, c)
    signal.signal(signal.SIGWINCH, _resize)

    out_fd = sys.stdout.fileno()
    try:
        while True:
            if proc.poll() is not None:
                break
            try:
                rl, _, _ = select.select([fd_in, mfd], [], [], 0.05)
            except (OSError, select.error):
                break

            if fd_in in rl:
                try:
                    data = os.read(fd_in, 4096)
                except OSError:
                    break
                if not data:
                    break
                data = _handle_paste(data)
                try:
                    os.write(mfd, data)
                except OSError:
                    break

            if mfd in rl:
                try:
                    data = os.read(mfd, 4096)
                    if data:
                        os.write(out_fd, data)
                except OSError:
                    break
    finally:
        _log(f'select loop exited: codex poll={proc.poll()}')
        try:
            termios.tcsetattr(fd_in, termios.TCSADRAIN, old)
        except Exception:
            pass
        try:
            os.close(mfd)
        except Exception:
            pass
    proc.wait()
    sys.exit(proc.returncode or 0)

if __name__ == '__main__':
    main()
PYEOF
    sed -i "s|__CONSTRUCT_REAL_CODEX__|${real_target}|" "$wrapper_path"
    chmod +x "$wrapper_path"
    dbg "Codex PTY wrapper v2 installed at $wrapper_path (real: $real_target)"
}

patch_gemini_paste_wrapper() {
    local active_gemini
    active_gemini=$(command -v gemini 2>/dev/null || true)

    if [[ -z "$active_gemini" ]]; then
        dbg "gemini not found in PATH — skipping wrapper install"
        return
    fi

    local real_target
    real_target=$(find_real_npm_binary "gemini" "@google/gemini-cli")
    if [[ -z "$real_target" ]]; then
        dbg "Cannot find real gemini binary in npm-global — skipping wrapper install"
        return
    fi
    dbg "Real gemini target: $real_target"

    local wrapper_path="$active_gemini"
    if [[ "$wrapper_path" == "$real_target" ]]; then
        if [[ -d /home/linuxbrew/.linuxbrew/bin ]] && [[ -w /home/linuxbrew/.linuxbrew/bin ]]; then
            wrapper_path="/home/linuxbrew/.linuxbrew/bin/gemini"
        else
            wrapper_path="$HOME/.local/bin/gemini"
            mkdir -p "$HOME/.local/bin"
        fi
    fi

    if grep -q "construct-gemini-wrapper-v1" "$wrapper_path" 2>/dev/null; then
        dbg "Gemini PTY wrapper v1 already at $wrapper_path"
        return
    fi

    rm -f "${wrapper_path}-real"
    rm -f "$wrapper_path"

    dbg "Installing Gemini clipboard PTY wrapper at $wrapper_path (real: $real_target)"
    cat > "$wrapper_path" << 'PYEOF'
#!/home/linuxbrew/.linuxbrew/bin/python3
# construct-gemini-wrapper-v1
# PTY interceptor: catches Ctrl+V and injects @image file path into Gemini input.
import fcntl, os, pty, select, signal, struct, subprocess, sys, termios, time, tty

_URL   = os.environ.get('CONSTRUCT_CLIPBOARD_URL', '')
_TOKEN = os.environ.get('CONSTRUCT_CLIPBOARD_TOKEN', '')
_DIR   = '.construct-clipboard'
_LOGDIR = os.path.expanduser('~/.config/construct-cli/logs')
_LOG    = os.path.join(_LOGDIR, 'construct-gemini-wrapper.log')
_REAL   = '__CONSTRUCT_REAL_GEMINI__'

def _log(msg):
    try:
        os.makedirs(_LOGDIR, exist_ok=True)
        with open(_LOG, 'a') as f:
            f.write(f'[{time.strftime("%H:%M:%S")}] {msg}\n')
    except Exception:
        pass

def _save_image():
    _log(f'ctrl+v detected: url_set={bool(_URL)} token_set={bool(_TOKEN)}')
    if not _URL or not _TOKEN:
        _log('save_image: bridge env not set — cannot fetch')
        return None
    try:
        os.makedirs(_DIR, exist_ok=True)
    except Exception:
        return None
    ts  = int(time.time() * 1000)
    img = os.path.abspath(f'{_DIR}/clipboard-{ts}.png')
    r = subprocess.run(
        ['curl', '-sSf', '-H', f'X-Construct-Clip-Token: {_TOKEN}',
         f'{_URL}/paste?type=image/png', '-o', img],
        capture_output=True, timeout=5,
    )
    if r.returncode != 0 or not os.path.exists(img) or os.path.getsize(img) == 0:
        try:
            os.remove(img)
        except Exception:
            pass
        _log(f'save_image: fetch failed rc={r.returncode} stderr={r.stderr[:200]}')
        return None
    _log(f'save_image: saved {img} ({os.path.getsize(img)} bytes)')
    return img

_PASTE_TRIGGERS = [b'\x16', b'\x1b[118;5u', b'\x1b[118;9u', b'\x1b[200~']

def _handle_paste(data):
    # If bracketed paste start is found, we might want to skip until end
    # but for now let's just treat it as a trigger.
    if not any(t in data for t in _PASTE_TRIGGERS):
        return data
    out = b''
    while data:
        earliest_idx = None
        earliest_seq = None
        for seq in _PASTE_TRIGGERS:
            idx = data.find(seq)
            if idx >= 0 and (earliest_idx is None or idx < earliest_idx):
                earliest_idx = idx
                earliest_seq = seq
        if earliest_idx is None:
            out += data
            break
        
        # Add everything before the trigger
        out += data[:earliest_idx]
        # Skip the trigger in the input data
        data = data[earliest_idx + len(earliest_seq):]
        
        # Try to fetch image
        img = _save_image()
        if img:
            # If it was bracketed paste, we should also try to consume until \x1b[201~
            if earliest_seq == b'\x1b[200~':
                end_idx = data.find(b'\x1b[201~')
                if end_idx >= 0:
                    data = data[end_idx + 6:] # Skip content and \x1b[201~
            
            # Inject path (Gemini/Qwen/Cline/Pi/Claude/Copilot usually want @path)
            # Codex wants raw path.
            # This wrapper is for gemini, so we use @.
            out += f'@{img} '.encode()
            _log(f'injected @{img}')
        else:
            _log('no image — forwarding raw trigger')
            out += earliest_seq
    return out

def _winsz(fd):
    try:
        buf = fcntl.ioctl(fd, termios.TIOCGWINSZ, b'\x00' * 8)
        return struct.unpack('HHHH', buf)[:2]
    except Exception:
        return (24, 80)

def _setwinsz(fd, rows, cols):
    try:
        fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack('HHHH', rows, cols, 0, 0))
    except Exception:
        pass

def main():
    real = _REAL
    if not os.path.isfile(real) or not os.access(real, os.X_OK):
        _log(f'ERROR: real binary not found or not executable: {real}')
        sys.exit(1)
    args = [real] + sys.argv[1:]
    _log(f'starting: real={real} url_set={bool(_URL)} token_set={bool(_TOKEN)} args={sys.argv[1:]}')

    fd_in = sys.stdin.fileno()
    try:
        old = termios.tcgetattr(fd_in)
    except termios.error:
        _log('not a tty — exec direct (no interception)')
        os.execv(real, args)
        return

    mfd, sfd = pty.openpty()
    r, c = _winsz(sys.stdout.fileno())
    _setwinsz(sfd, r, c)

    proc = subprocess.Popen(args, stdin=sfd, stdout=sfd, stderr=sfd,
                            close_fds=True, start_new_session=True)
    os.close(sfd)
    tty.setraw(fd_in)
    _log(f'pty ready: pid={proc.pid}')

    def _resize(sig, frame):
        r, c = _winsz(sys.stdout.fileno())
        _setwinsz(mfd, r, c)
    signal.signal(signal.SIGWINCH, _resize)

    out_fd = sys.stdout.fileno()
    try:
        while True:
            if proc.poll() is not None:
                break
            try:
                rl, _, _ = select.select([fd_in, mfd], [], [], 0.05)
            except (OSError, select.error):
                break

            if fd_in in rl:
                try:
                    data = os.read(fd_in, 4096)
                except OSError:
                    break
                if not data:
                    break
                data = _handle_paste(data)
                try:
                    os.write(mfd, data)
                except OSError:
                    break

            if mfd in rl:
                try:
                    data = os.read(mfd, 4096)
                    if data:
                        os.write(out_fd, data)
                except OSError:
                    break
    finally:
        _log(f'select loop exited: gemini poll={proc.poll()}')
        try:
            termios.tcsetattr(fd_in, termios.TCSADRAIN, old)
        except Exception:
            pass
        try:
            os.close(mfd)
        except Exception:
            pass
    proc.wait()
    sys.exit(proc.returncode or 0)

if __name__ == '__main__':
    main()
PYEOF
    sed -i "s|__CONSTRUCT_REAL_GEMINI__|${real_target}|" "$wrapper_path"
    chmod +x "$wrapper_path"
    dbg "Gemini PTY wrapper v1 installed at $wrapper_path (real: $real_target)"
}

ensure_xvfb() {
    # If clipboard image patch is disabled, don't start Xvfb.
    if [[ "${CONSTRUCT_CLIPBOARD_IMAGE_PATCH:-1}" == "0" ]]; then
        return
    fi

    # Ensure DISPLAY is set (default to :0 if missing)
    if [[ -z "$DISPLAY" ]]; then
        export DISPLAY="${CONSTRUCT_X11_DISPLAY:-:0}"
    fi

    local display_num="${DISPLAY#:}"
    local x_sock="/tmp/.X11-unix/X${display_num}"
    local x_lock="/tmp/.X${display_num}-lock"

    # If the socket is missing, we need to start Xvfb.
    if [[ ! -S "$x_sock" ]]; then
        if command -v Xvfb >/dev/null; then
            dbg "Starting Xvfb on $DISPLAY..."
            # Clean up stale locks that might prevent Xvfb from starting.
            rm -f "$x_lock" 2>/dev/null
            Xvfb "$DISPLAY" -screen 0 1024x768x24 -nolisten tcp >/tmp/xvfb.log 2>&1 &
            
            # Wait for Xvfb to be ready (up to 2 seconds).
            local count=0
            while [ $count -lt 20 ]; do
                if [ -S "$x_sock" ]; then
                    dbg "Xvfb ready on $DISPLAY"
                    break
                fi
                sleep 0.1
                count=$((count + 1))
            done
        else
            dbg "Xvfb not found; skipping headless X11 setup"
        fi
    fi
}

find_real_npm_binary() {
    local cmd="$1"
    local pkg="$2"
    local npm_bin
    npm_bin=$(npm bin -g 2>/dev/null || true)
    
    # Priority 1: Check in npm-global/lib/node_modules (authoritative location)
    local lib_path="$HOME/.npm-global/lib/node_modules/$pkg"
    if [[ -d "$lib_path" ]]; then
        local bin_rel
        bin_rel=$(jq -r ".bin[\"$cmd\"] // .bin" "$lib_path/package.json" 2>/dev/null || true)
        if [[ -n "$bin_rel" ]] && [[ "$bin_rel" != "null" ]]; then
            echo "$lib_path/$bin_rel"
            return
        fi
    fi

    # Priority 2: Candidates in npm-global/bin that are NOT already our wrapper.
    for candidate in "$HOME/.npm-global/bin/$cmd" "${npm_bin}/$cmd"; do
        if [[ -f "$candidate" ]] && ! grep -q "construct-.*-wrapper" "$candidate" 2>/dev/null; then
            echo "$candidate"
            return
        fi
    done
    
    # Priority 3: Search within the package directory for the binary name.
    if [[ -d "$lib_path" ]]; then
        find -L "$lib_path" -type f -name "$cmd" 2>/dev/null | head -1
    fi
}

echo "🔧 Patching clipboard support for agents..."
ensure_xvfb
fix_clipboard_libs
echo "🔧 Patching Copilot clipboard bridge..."
patch_copilot_clipboard
echo "🔧 Patching Copilot image paste keybinding..."
patch_copilot_keybinding
echo "🔧 Patching agent code for cross-platform clipboard..."
patch_agent_code
echo "🔧 Installing Copilot clipboard PTY wrapper..."
patch_copilot_paste_wrapper
echo "🔧 Installing Codex clipboard PTY wrapper..."
patch_codex_paste_wrapper
echo "🔧 Installing Gemini clipboard PTY wrapper..."
patch_gemini_paste_wrapper
