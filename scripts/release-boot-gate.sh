#!/usr/bin/env bash
# Release boot gate: download a published construct release artifact,
# verify its checksum, and smoke it in an isolated HOME. Proves the
# published binary boots, reports the right version, and passes
# `sys doctor --json` before a release is trusted.
#
# Usage: scripts/release-boot-gate.sh [tag]
#   tag defaults to the latest published release (gh required).
# Requires: gh CLI authenticated against the release repo, network.
set -euo pipefail

TAG=${1:-}
fail() { echo "FAIL: $*" >&2; exit 1; }

command -v gh >/dev/null 2>&1 || fail "gh CLI not found"
# macOS ships shasum, not sha256sum; both consume the same checksums.txt format.
if command -v sha256sum >/dev/null 2>&1; then
	SUM=(sha256sum -c -)
elif command -v shasum >/dev/null 2>&1; then
	SUM=(shasum -a 256 -c -)
else
	fail "neither sha256sum nor shasum found"
fi

if [ -z "$TAG" ]; then
	TAG=$(gh release view --json tagName -q .tagName) || fail "could not resolve latest release tag"
fi
echo "boot gate: release $TAG"

# Strip a leading v if present; release assets are plain semver.
ASSET_TAG=${TAG#v}

case "$(uname -s)-$(uname -m)" in
	Linux-x86_64) ARCH_ASSET="construct-linux-amd64-${ASSET_TAG}.tar.gz" ;;
	Linux-aarch64) ARCH_ASSET="construct-linux-arm64-${ASSET_TAG}.tar.gz" ;;
	Darwin-arm64) ARCH_ASSET="construct-darwin-arm64-${ASSET_TAG}.tar.gz" ;;
	Darwin-x86_64) ARCH_ASSET="construct-darwin-amd64-${ASSET_TAG}.tar.gz" ;;
	*) fail "unsupported platform: $(uname -s)-$(uname -m)" ;;
esac

WORK=$(mktemp -d /var/tmp/construct-bootgate.XXXXXX)
trap 'rm -rf "$WORK"' EXIT

echo "downloading $ARCH_ASSET + checksums.txt..."
gh release download "$TAG" -p "$ARCH_ASSET" -p checksums.txt -D "$WORK" \
	|| fail "release download failed (asset $ARCH_ASSET on tag $TAG)"

# Checksum verification against the release's checksums.txt.
( cd "$WORK" && grep -F "$ARCH_ASSET" checksums.txt | "${SUM[@]}" ) \
	|| fail "checksum mismatch for $ARCH_ASSET"

tar -xzf "$WORK/$ARCH_ASSET" -C "$WORK"
BIN=""
for cand in "$WORK/construct" "$WORK/construct-linux-amd64" "$WORK/construct-linux-arm64" "$WORK/construct-darwin-arm64" "$WORK/construct-darwin-amd64"; do
	[ -f "$cand" ] && BIN="$cand" && break
done
[ -n "$BIN" ] || fail "no construct binary found in the archive: $(ls "$WORK")"
chmod +x "$BIN"

# Isolated HOME: the gate must never touch the real config dir.
export HOME=$(mktemp -d /var/tmp/construct-bootgate-home.XXXXXX)
trap 'rm -rf "$WORK" "$HOME"' EXIT

echo "smoke: version"
out=$("$BIN" --version 2>&1) || fail "binary did not run: $out"
case "$out" in
	*"$ASSET_TAG"*) echo "ok: version reports $out" ;;
	*) fail "version '$out' does not report release tag $ASSET_TAG" ;;
esac

echo "smoke: sys doctor --json"
json=$("$BIN" sys doctor --json 2>/dev/null) || fail "sys doctor --json exited non-zero"
printf '%s' "$json" | grep -q '"checks"' || fail "doctor output has no checks array"
printf '%s' "$json" | grep -q '"name"' || fail "doctor checks lack a name field"
printf '%s' "$json" | grep -q '"status"' || fail "doctor checks lack a status field"
echo "ok: doctor --json shape"

echo "release boot gate: PASS ($TAG)"
