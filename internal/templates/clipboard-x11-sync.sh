#!/usr/bin/env bash

# clipboard-x11-sync.sh - Sync host clipboard into X11 for headless apps.

HOST_URL="${CONSTRUCT_CLIPBOARD_URL}"
AUTH_TOKEN="${CONSTRUCT_CLIPBOARD_TOKEN}"
XCLIP_BIN="/usr/bin/xclip-real"
TMP_IMG="/tmp/construct-clipboard.png"
DEBUG="${CONSTRUCT_DEBUG:-0}"

# Poll interval in seconds. Agents using native X11 clipboard bindings (e.g.
# Copilot CLI via @teddyzhu/clipboard) read the X11 selection directly — a slow
# poll creates a race where the image isn't synced yet when the agent reads.
# 200ms keeps CPU usage negligible while closing that window.
SYNC_INTERVAL="${CONSTRUCT_CLIPBOARD_SYNC_INTERVAL:-0.2}"

dbg() {
    if [ "$DEBUG" = "1" ]; then
        echo "[x11-sync] $*" >&2
    fi
}

if [ -z "$HOST_URL" ] || [ -z "$AUTH_TOKEN" ]; then
    dbg "Missing HOST_URL or AUTH_TOKEN, exiting"
    exit 0
fi

if [ ! -x "$XCLIP_BIN" ]; then
    dbg "xclip-real not found at $XCLIP_BIN, exiting"
    exit 0
fi

dbg "Starting sync loop: interval=${SYNC_INTERVAL}s url=${HOST_URL} display=${DISPLAY}"

while true; do
    # Try image clipboard first.
    HTTP_CODE=$(curl -s -o "$TMP_IMG" -w "%{http_code}" \
        -H "X-Construct-Clip-Token: ${AUTH_TOKEN}" \
        "${HOST_URL}/paste?type=image/png" 2>/dev/null)
    CURL_EXIT=$?

    if [ "$CURL_EXIT" -eq 0 ] && [ "$HTTP_CODE" = "200" ] && [ -s "$TMP_IMG" ]; then
        IMG_SIZE=$(wc -c < "$TMP_IMG")
        dbg "Image fetched: ${IMG_SIZE} bytes, loading into X11"
        if "$XCLIP_BIN" -selection clipboard -t image/png -i "$TMP_IMG" >/dev/null 2>&1; then
            dbg "Image loaded into X11 clipboard OK"
        else
            dbg "xclip-real failed to load image (exit=$?)"
        fi
        sleep "$SYNC_INTERVAL"
        continue
    else
        dbg "No image: curl_exit=${CURL_EXIT} http=${HTTP_CODE}"
    fi

    # Fallback to text clipboard.
    TEXT=$(curl -s -f -H "X-Construct-Clip-Token: ${AUTH_TOKEN}" \
        "${HOST_URL}/paste?type=text/plain" 2>/dev/null || true)
    if [ -n "$TEXT" ]; then
        dbg "Text synced: ${#TEXT} chars"
        printf "%s" "$TEXT" | "$XCLIP_BIN" -selection clipboard -t text/plain -i >/dev/null 2>&1 || true
    fi

    sleep "$SYNC_INTERVAL"
done
