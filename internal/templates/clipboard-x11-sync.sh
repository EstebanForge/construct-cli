#!/usr/bin/env bash

# clipboard-x11-sync.sh - Sync host clipboard into X11 for headless apps.

HOST_URL="${CONSTRUCT_CLIPBOARD_URL}"
AUTH_TOKEN="${CONSTRUCT_CLIPBOARD_TOKEN}"
XCLIP_BIN="/usr/bin/xclip-real"
TMP_IMG="/tmp/construct-clipboard.png"

if [ -z "$HOST_URL" ] || [ -z "$AUTH_TOKEN" ]; then
    exit 0
fi

if [ ! -x "$XCLIP_BIN" ]; then
    exit 0
fi

while true; do
    # Try image clipboard first.
    if curl -s -f -H "X-Construct-Clip-Token: ${AUTH_TOKEN}" \
        "${HOST_URL}/paste?type=image/png" -o "$TMP_IMG"; then
        if [ -s "$TMP_IMG" ]; then
            "$XCLIP_BIN" -selection clipboard -t image/png -i "$TMP_IMG" >/dev/null 2>&1 || true
            sleep 1
            continue
        fi
    fi

    # Fallback to text clipboard.
    TEXT=$(curl -s -f -H "X-Construct-Clip-Token: ${AUTH_TOKEN}" \
        "${HOST_URL}/paste?type=text/plain" 2>/dev/null || true)
    if [ -n "$TEXT" ]; then
        printf "%s" "$TEXT" | "$XCLIP_BIN" -selection clipboard -t text/plain -i >/dev/null 2>&1 || true
    fi

    sleep 1
done
