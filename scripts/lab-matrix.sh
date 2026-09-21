#!/usr/bin/env bash
# construct microVM lab matrix — unattended E2E against an isolated HOME.
#
# Runs the microVM-engine smoke matrix (cold, exec, warm, reconnect,
# unmapped fail-closed) against a scratch construct home that shares only
# the host msb runtime (bin/lib) while keeping its own config, db and
# image store. Nothing touches the real config; all state lives under the
# lab home so repeated runs are hermetic (the first run pays one image
# pull into the lab store; later runs reuse it).
#
# Usage: scripts/lab-matrix.sh [lab_home]
#   lab_home defaults to /var/tmp/construct-lab/home
#   BIN overrides the construct binary (default ~/.local/bin/construct)
#
# Requires: host msb installed with libkrunfw (msb --version works), KVM.
set -euo pipefail

REAL_HOME=${HOME:?HOME must be set}
BIN=${BIN:-$REAL_HOME/.local/bin/construct}
LAB=${1:-/var/tmp/construct-lab/home}
LOG="$LAB/.config/construct-cli/logs"

fail() { echo "FAIL: $*" >&2; exit 1; }
[ -x "$BIN" ] || fail "construct binary not found at $BIN"
msb --version >/dev/null 2>&1 || fail "host msb CLI not found"

mkdir -p "$LAB/.config/construct-cli" "$LAB/.microsandbox" "$LAB/projA" "$LAB/projB" "$LAB/projC"
printf '[runtime]\nbackend = "microvm"\n' > "$LAB/.config/construct-cli/config.toml"
ln -sfn "$REAL_HOME/.microsandbox/bin" "$LAB/.microsandbox/bin"
ln -sfn "$REAL_HOME/.microsandbox/lib" "$LAB/.microsandbox/lib"

# All construct invocations below run with HOME pointed at the lab so
# config, roots.json, telemetry logs and the msb store stay hermetic.
export HOME="$LAB"

# Pre-grant consent for projA/projB (documented roots.json format) so an
# unattended run never hits the interactive learn gate. projC stays
# unlearned to prove the fail-closed unmapped path.
printf '{"version":1,"roots":[{"path":"%s","learned_at":"2026-01-01T00:00:00Z","last_used":"2026-01-01T00:00:00Z"},{"path":"%s","learned_at":"2026-01-01T00:00:00Z","last_used":"2026-01-01T00:00:00Z"}]}\n' \
	"$LAB/projA" "$LAB/projB" > "$LAB/.config/construct-cli/roots.json"

last_outcome() { tail -1 "$LOG/msb-boot.log" 2>/dev/null | sed 's/.*outcome=\([a-z]*\).*/\1/'; }
assert_outcome() { [ "$(last_outcome)" = "$1" ] || fail "expected outcome=$1, got: $(last_outcome)"; echo "ok: $1"; }

cd "$LAB/projA"

# 1. COLD (or recreate once after a binary upgrade migrates the mount-set
#    hash label, or reconnect when the lab daemon already runs on the same
#    build — all three are healthy).
"$BIN" sys daemon start >/dev/null 2>&1
out=$(last_outcome)
case "$out" in cold|recreate|reconnect) echo "ok: $out";; *) fail "expected cold|recreate|reconnect, got $out";; esac

# 2. EXEC through the mapped workdir.
"$BIN" sys exec -- /bin/uname -sm >/dev/null 2>&1 </dev/null || fail "sys exec failed"
echo "ok: exec"

# 3. WARM: stop then start.
"$BIN" sys daemon stop >/dev/null 2>&1
sleep 2
"$BIN" sys daemon start >/dev/null 2>&1
assert_outcome warm

# 4. RECONNECT.
"$BIN" sys daemon start >/dev/null 2>&1
assert_outcome reconnect

# 5. UNMAPPED fail-closed.
cd "$LAB/projC"
if "$BIN" sys exec -- echo x </dev/null >/dev/null 2>&1; then
	fail "exec from an unmapped directory must fail closed"
fi
echo "ok: unmapped fail-closed"

# 6. Stop the daemon; leave the lab clean for the next run.
cd "$LAB/projA"
"$BIN" sys daemon stop >/dev/null 2>&1

echo "lab matrix: PASS"
