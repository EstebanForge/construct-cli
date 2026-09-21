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
# Resolve BIN to an absolute path: the matrix cd's into the lab home, which
# would break a relative override (./bin/construct -> not found).
case $BIN in
	/*) ;;
	*/*) BIN=$PWD/${BIN#./} ;;
	*) BIN=$(command -v "$BIN" 2>/dev/null || echo "$PWD/$BIN") ;;
esac
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
#    hash label, or reconnect/warm when the lab daemon already runs on the
#    same build — all four are healthy; the matrix is re-entrant).
"$BIN" sys daemon start >/dev/null 2>&1
out=$(last_outcome)
case "$out" in cold|recreate|reconnect|warm) echo "ok: $out";; *) fail "expected cold|recreate|reconnect|warm, got $out";; esac

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

# 5b. BAKED-IMAGE STAGE — gracefully skipped on pre-bake images (detection:
# claude resolving to /usr/local/bin proves the bake; on old images claude
# lives in the bind or is absent in a fresh lab home).
cd "$LAB/projA"
baked_claude=$("$BIN" sys exec -- sh -c 'command -v claude' </dev/null 2>/dev/null | tail -1 || true)
if [ "$baked_claude" = "/usr/local/bin/claude" ]; then
	# a. All five core agents resolve to the baked tier.
	for c in claude codex agy pi opencode; do
		p=$("$BIN" sys exec -- sh -c "command -v $c" </dev/null 2>/dev/null | tail -1 || true)
		[ "$p" = "/usr/local/bin/$c" ] || fail "baked agent $c resolves to '$p', want /usr/local/bin/$c"
	done
	echo "ok: baked agents resolve to /usr/local/bin"

	# b. Warm boots skip the install phase (gate lives on the root fs).
	"$BIN" sys daemon stop >/dev/null 2>&1
	sleep 2
	"$BIN" sys daemon start >/dev/null 2>&1
	assert_outcome warm
	ph=$("$BIN" sys exec -- cat /home/construct/.local/.construct_install_phase </dev/null 2>/dev/null || true)
	case "$ph" in
		install_ran=0*) echo "ok: warm boot skips install ($ph)";;
		*) fail "expected install_ran=0 on warm boot, got: '$ph'";;
	esac

	# c. Migration sweep: seed stale pre-bake copies, clear the marker,
	#    force a reinstall pass; the sweep must clear them and re-mark.
	"$BIN" sys exec -- sh -c 'mkdir -p ~/.local/bin ~/.opencode/bin; printf "#!/bin/sh\n" > ~/.local/bin/claude; chmod +x ~/.local/bin/claude; printf "#!/bin/sh\n" > ~/.opencode/bin/opencode; chmod +x ~/.opencode/bin/opencode' </dev/null >/dev/null 2>&1
	"$BIN" sys exec -- sh -c 'rm -f ~/.local/.construct_bake_migration_v1; touch ~/.local/.force_entrypoint' </dev/null >/dev/null 2>&1
	"$BIN" sys daemon stop >/dev/null 2>&1
	sleep 2
	"$BIN" sys daemon start >/dev/null 2>&1
	for f in /home/construct/.local/bin/claude /home/construct/.opencode/bin/opencode; do
		if "$BIN" sys exec -- sh -c "[ -e '$f' ]" </dev/null >/dev/null 2>&1; then
			fail "migration sweep left stale copy: $f"
		fi
	done
	"$BIN" sys exec -- sh -c '[ -f ~/.local/.construct_bake_migration_v1 ]' </dev/null >/dev/null 2>&1 || fail "bake migration marker missing after sweep"
	ph=$("$BIN" sys exec -- cat /home/construct/.local/.construct_install_phase </dev/null 2>/dev/null || true)
	case "$ph" in
		install_ran=1*) : ;;
		*) fail "forced pass did not run the install (got: '$ph')";;
	esac
	echo "ok: migration sweep cleared stale copies"

	# d. Override shadow: declaring codex in packages.toml must reinstate a
	#    bind copy that wins by PATH.
	printf '[npm]\npackages = ["@openai/codex@latest"]\n' > "$LAB/.config/construct-cli/packages.toml"
	"$BIN" sys exec -- sh -c 'touch ~/.local/.force_entrypoint' </dev/null >/dev/null 2>&1
	"$BIN" sys daemon stop >/dev/null 2>&1
	sleep 2
	"$BIN" sys daemon start >/dev/null 2>&1
	p=$("$BIN" sys exec -- sh -c 'command -v codex' </dev/null 2>/dev/null | tail -1 || true)
	case "$p" in */.npm-global/bin/codex) echo "ok: packages.toml codex shadows baked";; *) fail "codex override not shadowing, got: '$p'";; esac

	# e. Override removal: drop the entry, clear the bind copy (documented
	#    one-liner), verify the baked binary serves again.
	printf '[npm]\npackages = []\n' > "$LAB/.config/construct-cli/packages.toml"
	"$BIN" sys exec -- sh -c 'rm -rf ~/.npm-global/bin/codex ~/.npm-global/lib/node_modules/@openai/codex' </dev/null >/dev/null 2>&1
	p=$("$BIN" sys exec -- sh -c 'command -v codex' </dev/null 2>/dev/null | tail -1 || true)
	[ "$p" = "/usr/local/bin/codex" ] || fail "codex did not fall back to baked, got: '$p'"
	echo "ok: removing override falls back to baked codex"
else
	echo "skip: baked-image stage (pre-bake image: claude at '$baked_claude')"
fi

# 5c. DOCTOR STAGE — migration checks against the host (requires a reachable
# docker daemon AND a BIN that carries the doctor migration checks).
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1 && "$BIN" sys doctor --json 2>/dev/null | grep -q 'Stale Packages Volume'; then
	docker volume create labdoctest_construct-packages >/dev/null 2>&1 || true
	doc=$("$BIN" sys doctor --fix 2>/dev/null || true)
	echo "$doc" | grep -q 'Stale Packages Volume: Removed stale volume' || fail "doctor --fix did not remove the stale fixture volume"
	if docker volume ls --format '{{.Name}}' | grep -q '^labdoctest_construct-packages$'; then
		fail "stale fixture volume survived doctor --fix"
	fi
	echo "ok: doctor --fix cleaned the stale volume fixture"
else
	echo "skip: doctor volume stage (no docker or BIN predates migration checks)"
fi

# 6. Stop the daemon; leave the lab clean for the next run.
cd "$LAB/projA"
"$BIN" sys daemon stop >/dev/null 2>&1

echo "lab matrix: PASS"
