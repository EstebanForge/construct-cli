# Clipboard Bridge Debug Analysis

**Date:** 2026-02-06
**Context:** macOS host running Codex inside construct-cli container
**Issue:** Spotty clipboard bridge behavior - "no image in clipboard" or "x11 server wasn't responding" errors despite valid clipboard content

---

## Executive Summary

The clipboard bridge in construct-cli has **two critical reliability issues** when running Codex in container mode on macOS:

1. **No timeouts** - `osascript`, `curl`, and HTTP server calls can hang indefinitely
2. **osascript is unreliable** - The `get clipboard as «class PNGf»` command is known to be flaky

The WSL/.exe bridge architecture is **still needed** for container isolation, but requires reliability improvements.

---

## Architecture Overview

### Construct-CLI Clipboard Bridge (macOS Host → Container)

```
┌─────────────────────────────────────────────────────────────────────┐
│                        macOS Host                                │
│  ┌───────────────────────────────────────────────────────────────┐  │
│  │  Clipboard HTTP Server (Go)                              │  │
│  │  - Listens on random port                                │  │
│  │  - Uses osascript to read clipboard                       │  │
│  │  - NO TIMEOUT (BUG #1)                                │  │
│  └───────────────────────────────────────────────────────────────┘  │
│                          ↓ HTTP Request                         │
└─────────────────────────────────────────────────────────────────────┘
└─────────────────────────────────────────────────────────────────────┐
│                       Container (Docker/Podman)                    │
│  ┌───────────────────────────────────────────────────────────────┐  │
│  │  Fake powershell.exe                                     │  │
│  │  - Calls construct-cli HTTP server                          │  │
│  │  - Uses curl (NO TIMEOUT - BUG #2)                      │  │
│  └───────────────────────────────────────────────────────────────┘  │
│                          ↓                                    │
│  ┌───────────────────────────────────────────────────────────────┐  │
│  │  Codex (Rust TUI)                                      │  │
│  │  - Reads image from powershell.exe output                      │  │
│  │  - Uses arboard crate (would use native if not container)  │  │
│  └───────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘
```

---

## Codex's Clipboard Implementation

### Direct From Source (`/tmp/codex/codex-rs/tui/src/clipboard_paste.rs`)

**Primary method (via arboard crate):**
```rust
#[cfg(not(target_os = "android"))]
pub fn paste_image_as_png() -> Result<(Vec<u8>, PastedImageInfo), PasteImageError> {
    let mut cb = arboard::Clipboard::new()
        .map_err(|e| PasteImageError::ClipboardUnavailable(e.to_string()))?;
    
    // Try files first (Finder, etc.), then image data
    let files = cb.get().file_list()...
    let dyn_img = if let Some(img) = files...find_map(|f| image::open(f).ok()) {
        // Load from file path
    } else {
        // Load from clipboard image data directly
        let img = cb.get_image()...;
        // ...
    };
    
    // Encode to PNG
    Ok((png, info))
}
```

**WSL Fallback (only when detected):**
```rust
#[cfg(target_os = "linux")]
fn try_wsl_clipboard_fallback(error: &PasteImageError) -> Result<...> {
    if !is_probably_wsl() || !matches!(error, ClipboardUnavailable(_) | NoImage(_)) {
        return Err(error.clone());
    }
    
    // Call PowerShell, convert Windows path to WSL path
    let Some(win_path) = try_dump_windows_clipboard_image() else {
        return Err(error.clone());
    };
    // ...
}
```

**WSL Detection:**
```rust
fn is_probably_wsl() -> bool {
    // Check /proc/version for "microsoft" or "WSL"
    if let Ok(version) = std::fs::read_to_string("/proc/version") {
        if version.to_lowercase().contains("microsoft") || 
           version.to_lowercase().contains("wsl") {
            return true;
        }
    }
    // Check WSL environment variables
    std::env::var_os("WSL_DISTRO_NAME").is_some() || 
    std::env::var_os("WSL_INTEROP").is_some()
}
```

### Recent Codex Changes (Last 30 Days)

| Date | PR | Description |
|------|-----|-------------|
| Jan 18, 2026 | #9473 | "Fixed TUI regression related to image paste in WSL" - Fixed quoted path handling |
| Jan 15, 2026 | #9318 | "Revert empty paste image handling" - Reverted #9049 due to instability |
| Jan 12, 2026 | #9049 | "Handle image paste from empty paste events" - Attempted to handle terminals without paste signals |

**Takeaway:** Codex has had clipboard stability issues recently, suggesting the arboard/WL-paste path is inherently tricky.

---

## Construct-CLI Clipboard Implementation

### Current Code (`internal/clipboard/clipboard.go`)

**macOS image retrieval:**
```go
func getMacImage() ([]byte, error) {
    script := "get the clipboard as «class PNGf»"
    cmd := exec.Command("osascript", "-e", script)
    output, err := cmd.CombinedOutput()  // ← NO TIMEOUT (BUG #1)
    
    if err != nil {
        return nil, ErrNoImage
    }
    
    // Parse hex output «data PNGf89504E47...»
    // Decode to PNG bytes
    // ...
}
```

**HTTP Server (`internal/clipboard/server.go`):**
```go
func (s *Server) serve() {
    mux := http.NewServeMux()
    mux.HandleFunc("/paste", s.handlePaste)
    
    if err := http.Serve(s.listener, mux); err != nil {
        logf("[Clipboard Server] serve error: %v\n", err)
    }
    // ← NO TIMEOUT configured on server (BUG #3)
}
```

**Fake powershell.exe (`internal/templates/powershell.exe`):**
```bash
#!/bin/bash
# Fake powershell.exe for codex WSL clipboard fallback

HOST_URL="${CONSTRUCT_CLIPBOARD_URL}"
AUTH_TOKEN="${CONSTRUCT_CLIPBOARD_TOKEN}"
TMP_PNG="${CLIP_DIR}/clipboard-$(date +%s%3N).png"

# curl without timeout
if curl -s -f \
    -H "X-Construct-Clip-Token: ${AUTH_TOKEN}" \
    "${HOST_URL}/paste?type=image/png" -o "$ABS_PNG"; then
    # Success
else
    # Failed
    exit 1
fi
```

---

## Root Causes Identified

### BUG #1: No Timeout on osascript

**Location:** `internal/clipboard/clipboard.go:getMacImage()`

**Problem:** `cmd.CombinedOutput()` waits indefinitely if osascript hangs.

**Common osascript hang scenarios:**
- Large images on clipboard (hex string is huge, slow to generate)
- Mixed content (text + image) causes unexpected clipboard state
- macOS NSPasteboard API latency spikes
- User switches clipboard content during read

**Impact:** User waits forever, then sees timeout error from Codex side.

### BUG #2: No Timeout on curl

**Location:** `internal/templates/powershell.exe`

**Problem:** `curl -s -f` has no `--max-time` argument.

**Impact:** If clipboard server hangs or is unresponsive, the container-side script waits forever.

### BUG #3: No Server Timeout

**Location:** `internal/clipboard/server.go:serve()`

**Problem:** `http.Serve()` uses default `http.Server` with no timeouts configured.

**Impact:** HTTP connections can hang indefinitely if network issues occur.

### ISSUE #4: osascript is Inherently Unreliable

Research confirms `osascript -e "get the clipboard as «class PNGf»"` is flaky:

- **Format issues:** Output varies between macOS versions
- **Performance:** Hex string generation is slow for large images
- **Error handling:** Returns empty output or partial data on clipboard edge cases
- **Alternative tools exist:** `pngpaste` (dedicated) is more reliable

---

## Recommended Fixes

### Fix #1: Add Timeout to osascript (Critical)

**File:** `internal/clipboard/clipboard.go`

```go
func getMacImage() ([]byte, error) {
    script := "get the clipboard as «class PNGf»"
    cmd := exec.Command("osascript", "-e", script)
    
    // Add 10-second timeout
    var output []byte
    var err error
    
    done := make(chan error, 1)
    go func() {
        output, err = cmd.CombinedOutput()
        done <- err
    }()
    
    select {
    case err := <-done:
        if err != nil {
            return nil, ErrNoImage
        }
    case <-time.After(10 * time.Second):
        if cmd.Process != nil {
            cmd.Process.Kill()
        }
        return nil, ErrNoImage
    }
    
    // Continue with existing parsing logic...
    trimmed := bytes.TrimSpace(output)
    if len(trimmed) == 0 {
        return nil, ErrNoImage
    }
    
    startMarker := []byte("«data PNGf»")
    // ... rest of existing code
}
```

### Fix #2: Add Timeout to curl (Critical)

**File:** `internal/templates/powershell.exe`

```bash
# Add --max-time 10 to prevent indefinite hangs
if curl -s -f --max-time 10 \
    -H "X-Construct-Clip-Token: ${AUTH_TOKEN}" \
    "${HOST_URL}/paste?type=image/png" -o "$ABS_PNG"; then
    SIZE=$(wc -c < "$ABS_PNG" 2>/dev/null || echo "0")
    debug_log "SUCCESS: Fetched $SIZE bytes"
    
    # ... rest of existing code
else
    debug_log "FAILED: curl returned error $?"
    rm -f "$ABS_PNG"
    exit 1
fi
```

### Fix #3: Configure HTTP Server Timeout (Important)

**File:** `internal/clipboard/server.go`

```go
func (s *Server) serve() {
    mux := http.NewServeMux()
    mux.HandleFunc("/paste", s.handlePaste)
    
    // Configure server with timeouts
    server := &http.Server{
        Handler:      mux,
        ReadTimeout:  15 * time.Second,
        WriteTimeout: 15 * time.Second,
        IdleTimeout:  30 * time.Second,
    }
    
    // Use the existing listener
    if err := server.Serve(s.listener); err != nil {
        logf("[Clipboard Server] serve error: %v\n", err)
    }
}
```

### Fix #4: Add pngpaste Fallback (Recommended)

**File:** `internal/clipboard/clipboard.go`

```go
func getMacImage() ([]byte, error) {
    // Try pngpaste first (more reliable tool)
    cmd := exec.Command("pngpaste")
    output, err := cmd.CombinedOutput()
    if err == nil && len(output) > 0 {
        // Verify it's actually PNG (magic bytes)
        if len(output) >= 8 && 
           bytes.Equal(output[:8], []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}) {
            debug_log("pngpaste succeeded")
            return output, nil
        }
    }
    
    // Fallback to osascript (existing logic)
    debug_log("pngpaste failed, trying osascript")
    script := "get the clipboard as «class PNGf»"
    // ... existing osascript code ...
}
```

Note: This requires adding `pngpaste` to `packages.toml`:
```toml
[[brew.package]]
name = "pngpaste"
```

### Fix #5: Enhanced Debug Logging (Diagnostic)

**File:** `internal/templates/powershell.exe`

Already has basic logging, but should include:
- Timing info
- File size validation
- Error details

```bash
debug_log "Starting clipboard fetch..."
START_TIME=$(date +%s)

# ... curl command ...

END_TIME=$(date +%s)
ELAPSED=$((END_TIME - START_TIME))
debug_log "Clipboard fetch completed in ${ELAPSED}s, size: ${SIZE} bytes"

# Validate PNG header
if [ -f "$ABS_PNG" ]; then
    HEADER=$(xxd -l 8 -p "$ABS_PNG" 2>/dev/null || echo "unknown")
    if [ "$HEADER" != "89504e47" ]; then
        debug_log "WARNING: Invalid PNG header: $HEADER"
    fi
fi
```

---

## Debugging Steps for User

### Enable Debug Logging

```bash
export CONSTRUCT_DEBUG=1
ct codex

# Check clipboard server logs:
cat ~/.config/construct-cli/logs/debug_clipboard_server.log
```

### Test clipboard tools directly

```bash
# Test osascript
osascript -e 'get the clipboard as «class PNGf»' | head -20

# Test pngpaste (if installed)
pngpaste | head -20

# Check what's actually on clipboard
osascript -e 'clipboard info'
```

### Reproduce Issue

1. Copy an image to clipboard (Cmd+C or screenshot)
2. Run: `export CONSTRUCT_DEBUG=1 && ct codex`
3. Paste in Codex (Ctrl+V)
4. Check logs for:
   - "Received paste request" entries
   - Timing between request and response
   - Size of data returned
   - Any error messages

---

## Summary Table

| Issue | Severity | Impact | Fix | Complexity |
|--------|------------|---------|------|
| No osascript timeout | **CRITICAL** | Can hang indefinitely | Low (5-10 lines) |
| No curl timeout | **CRITICAL** | Can hang indefinitely | Low (1 line) |
| No server timeout | **HIGH** | Connections can hang | Medium (restructure serve) |
| osascript unreliable | **MEDIUM** | Spotty success | Medium (add fallback) |
| Limited debug info | **LOW** | Hard to diagnose | Low (enhance logging) |

---

## Architecture Decision: Keep WSL Bridge

**Should we remove the WSL/.exe bridge?**

**Answer: NO** - The bridge is still needed for **container isolation**.

| Mode | Clipboard Access | Bridge Needed? |
|------|-----------------|----------------|
| Daemon (Container) | NO - no X11 socket | ✅ YES |
| Non-Daemon (Native Linux) | YES - arboard works | ❌ NO |
| macOS Host + Container | NO - no macOS APIs in container | ✅ YES |

**Key insight:** On macOS, containers **cannot** access host clipboard directly. There's no `/tmp/.X11-unix` or Wayland socket to mount. The HTTP bridge is the only reliable method.

**What needs to change:**
- Keep the bridge architecture
- Add timeouts to prevent hangs
- Make osascript more reliable (or use better tool)
- Enhance error handling and logging

---

## Files to Modify

1. `internal/clipboard/clipboard.go` - Add timeout to `getMacImage()`, consider pngpaste fallback
2. `internal/clipboard/server.go` - Add server timeouts
3. `internal/templates/powershell.exe` - Add `--max-time 10` to curl
4. `packages.toml` - Add `pngpaste` (optional, for better reliability)
5. `internal/config/config.go` - Add clipboard timeout configuration (optional)

---

## Testing Plan

After implementing fixes:

1. **Unit tests:** Test timeout behavior in clipboard package
2. **Integration test:** Run Codex in container, paste large images, verify timeout works
3. **Stress test:** Paste multiple times quickly, verify no race conditions
4. **Edge cases:** Test with empty clipboard, text-only, corrupted images
5. **Performance:** Verify timeouts don't cause false negatives

---

## Additional Considerations

### Alternative: Use arboard on Host

Instead of `osascript`, construct-cli could use the `arboard` crate directly:

```go
import "github.com/1-arf/arboard"

func getMacImage() ([]byte, error) {
    clipboard := arboard::Clipboard::new()
    image = clipboard.get_image()
    // Encode to PNG
    // ...
}
```

**Pros:**
- More reliable (same library Codex uses)
- Cross-platform (same code for all OS)
- Active development/maintenance

**Cons:**
- Adds Go dependency
- Requires compiling to macOS binary (no cross-compile from Linux)
- Larger binary size

**Recommendation:** Start with timeout fixes, consider arboard migration if osascript remains unreliable.

---

**End of Analysis**
