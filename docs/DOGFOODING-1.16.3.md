# Dogfood guide: 1.16.3

Companion to [VMsv2.md](VMsv2.md). Captures what to gather from a 1.16.3 dogfooding run, how to capture it, and what counts as a pass/fail. The data collected here is the input to P6 (snapshot fork gate) and the remaining P2 wire-in.

## Pre-flight (one-time)

Before you start gathering, verify the build is the right one:

```bash
construct --version   # expect: construct 1.16.3
ct sys doctor        # all checks OK
```

If the version is wrong, you are testing a different build. Stop and re-pull.

If the doctor surfaces a `Daemon Lock: held` warning while you are NOT running `ct`, investigate before proceeding — a stale lock means a previous `ct` crashed mid-acquire.

## P0 — Boot telemetry medians (GATES P6)

This is the one data point that actually unblocks future work. Without it, the P6 gate stays closed.

### What to capture

| Metric | Source | Why |
|---|---|---|
| Median cold boot (s) | `msb-boot: outcome=cold` lines | P6 gate input |
| Median recreate (s) | `msb-boot: outcome=recreate` lines | P6 gate input |
| Median warm boot (s) | `msb-boot: outcome=warm` lines | P6 gate input |
| Median reconnect (s) | `msb-boot: outcome=reconnect` lines | P6 gate input |
| Recreate frequency | count of `outcome=recreate` per week | P6 gate input |

### How to capture

```bash
# Per-outcome counts over the whole week
grep -h "msb-boot:" ~/.config/construct-cli/logs/*.log \
  | sed 's/.*outcome=//; s/ .*//' | sort | uniq -c | sort -rn

# Per-outcome seconds (raw distribution; eyeball the median)
for outcome in cold recreate warm reconnect; do
  echo "--- $outcome ---"
  grep -h "msb-boot:" ~/.config/construct-cli/logs/*.log \
    | grep "outcome=$outcome" \
    | grep -oE 'seconds=[0-9]+' \
    | sed 's/seconds=//' \
    | sort -n
done
```

For each outcome, the median is the middle value (or the average of the two middle values if you have an even count). Paste the medians into the table in `docs/VMsv2.md` section 10.

### What counts as a pass

The telemetry emits at every return path of `EnsureMsbDaemon`. If you see zero `msb-boot: outcome=cold` lines after a week, the cold path never fired — that is a bug, not a quiet daemon.

The P6 gate (per VMsv2.md section 4): gate opens if median cold > ~120s AND recreates occur in normal workflow. If your medians show median cold <60s, P6 is not worth the spike cost; skip the snapshot fork entirely.

## P2.8 — Learned roots

Validates the data layer (P2.1) + helper (P2.3) + CLI (P2.5/P2.6). The actual `EnsureMsbDaemon` wire-in (P2.2) is still pending; this test only confirms the registry + LRU + CLI work, NOT that the daemon mount set picks up the new root.

### What to capture

| Check | Expectation | Bug signal |
|---|---|---|
| `ct` from project B after project A | ONE `outcome=recreate` (the B visit) | more = race; zero = P2.2 not wired (known) |
| `ct` from project A again (after B) | NO recreate (reconnect) | recreate = P2.2 not wired |
| `ct` from project B again | NO recreate (reconnect) | recreate = P2.2 not wired |
| `ct sys daemon roots` | lists both A and B under "Daemon learned roots" | missing = registration broken |

### How to run

```bash
# Pre: two repos at different roots, single-path daemon (default backend,
# do NOT set multi_paths_enabled)
mkdir -p ~/Dev/proj-A ~/Dev/proj-B

cd ~/Dev/proj-A && ct pi "echo hi"     # cold create + installs
cd ~/Dev/proj-B && ct pi "echo hi"     # expect ONE recreate (learned root)
cd ~/Dev/proj-A && ct pi "echo hi"     # expect NO recreate (reconnect)
cd ~/Dev/proj-B && ct pi "echo hi"     # expect NO recreate (reconnect)
ct sys daemon roots                     # both /A and /B should appear

# Cap behavior: when the cap (default 8) is exceeded, oldest is evicted
# with one notice. To exercise this you need 9+ distinct projects; the
# CLI test above does not. Skip unless you have a real corpus.
```

## P4.3 — Image prepull

Validates the `MaybePrepullImage` spawn + the `msb pull` + `msb image tag` chain. The detached child writes to `~/.config/construct-cli/logs/prepull.log`.

### What to capture

| Check | Expectation | Bug signal |
|---|---|---|
| After `ct sys update`, prepull log exists | line: `prepull started` | missing = spawn failed silently |
| Prepull log outcome | `prepull done` (success) OR `prepull FAILED at pull/tag: <err>` | failure = capture the error verbatim |
| Next `ct sys doctor` image check | "ready" / "Image is up to date" | "Preparing microVM image" = prepull didn't seed the cache |
| `msb image ls` shows `construct-box:latest` after prepull | present | absent = pull or tag step failed |

### How to run

```bash
# Pre: remove the cached image so the prepull has work to do
msb image rm construct-box:latest

# Watch the prepull log live during update
tail -f ~/.config/construct-cli/logs/prepull.log &
ct sys update                           # self-update; prepull fires on success
# Wait for: "Spawned background prepull (log: ...)" then the log file fills

# After: verify the prepull log has the expected lines
tail -10 ~/.config/construct-cli/logs/prepull.log
# Expect: "prepull started" -> [msb pull output] -> "prepull done"
# OR:     "prepull FAILED at pull: ..." / "... at tag: ..." with the exact error.

# Now run any ct and confirm EnsureImage skips the pull path
ct sys doctor                           # image check should say "ready"
msb image ls | grep construct-box      # must show construct-box:latest present

# Edge case: when prepull_image = true but backend is not "microvm"
# the user should see a one-time warning. Set backend to "auto" (the
# default), set prepull_image = true, and run any ct:
# Expect: "prepull_image is enabled but backend=...; the prepull only
# fires for backend=microvm. Set backend=microvm or disable prepull_image."
```

## P7.6 — Skills mount

Validates the host skills library reaches every supported agent's `/home/construct/<agent>/skills`. Read-only by default; opt-in RW via `[sandbox] skills_read_only = false`.

### What to capture

| Check | Expectation | Bug signal |
|---|---|---|
| Mount present in sandbox | `ct pi "ls /home/construct/.claude/skills"` shows your skills dir contents | empty = mount missing or wrong target |
| Mount covers other agents | `/home/construct/.antigravity/skills`, `.config/crush/skills`, `.kilocode/skills` all populated | only one agent = agent list drift |
| Read-only enforcement | `ct pi "touch /home/construct/.claude/skills/write-test"` returns "Read-only file system" or similar | the file MUST NOT appear on the host afterwards (verify with `ls ~/Dev/EstebanForge/AGENTS/skills/write-test`) |
| Opt-in RW | with `skills_read_only = false` in config.toml + daemon restart, `ct pi "echo NEW > /home/construct/.claude/skills/write-test"` should succeed AND `cat ~/Dev/EstebanForge/AGENTS/skills/write-test` on the host should show "NEW" | the file must be visible on the host after the agent writes it |

### How to run

```bash
# Setup: ensure ~/Dev/EstebanForge/AGENTS/skills exists (it does in your env)
# List the candidate skills to confirm something is in there
ls ~/Dev/EstebanForge/AGENTS/skills/ | head -5

# Inside a ct session, check the mount shows up
ct pi "ls /home/construct/.claude/skills | head -5"
ct pi "ls /home/construct/.antigravity/skills | head -5"
ct pi "cat /home/construct/.claude/skills/acpx/SKILL.md | head -3"

# Verify RO: agent cannot write to the source
ct pi "touch /home/construct/.claude/skills/write-test 2>&1"
# Expect: "Read-only file system" (or similar) in the ct output
ls ~/Dev/EstebanForge/AGENTS/skills/write-test
# MUST say "No such file or directory" — the host source was not touched.

# Opt into RW and verify writes flow back
# Add to ~/.config/construct-cli/config.toml under [sandbox]:
#   skills_read_only = false
ct sys daemon restart                    # pick up the new config
ct pi "echo NEW > /home/construct/.claude/skills/write-test"
cat ~/Dev/EstebanForge/AGENTS/skills/write-test   # must show "NEW"
# Cleanup:
rm ~/Dev/EstebanForge/AGENTS/skills/write-test
# Restore skills_read_only = true, restart the daemon
```

## Macros worth flagging

These are not tied to a specific phase. If you hit any of them, capture the exact error and file as a follow-up.

### Concurrent `ct` invocations

Run two `ct` invocations in parallel from different terminals. Both should serialize cleanly via the new flock:

```bash
# Terminal 1
ct claude "long task"

# Terminal 2 (within the first 30s)
ct pi "echo hi"
```

Expect:
- The second `ct` waits up to 10 minutes for the flock (no error; the user sees a "waiting for another construct invocation" notice after 250ms).
- Both eventually run; the daemon is created once, not twice.

If you see "permission denied" on `~/.config/construct-cli/daemon.lock`, file it — that means the lock file's mode 0600 was clobbered.

### `ct sys doctor` regressions

After a fresh `construct build`, `ct sys doctor` should show:

- `Sandbox Mounts`: `All sandbox mount knobs healthy` (or appropriate warnings if `mount_home = true` or `host_binaries` is set)
- `Skills + Custom Override`: `No override foot-gun` UNLESS you explicitly set `allow_custom_compose_override = true` AND `mount_skills = true`
- `Daemon Lock`: `Daemon lock: free` (during normal `ct`; "held" only when a concurrent `ct` is running)
- The two new lines I added: `Live sessions: N` (N = 0 between runs, 1 during a `ct`)

If any of these regress, the round-8 follow-up introduced the bug; capture the doctor's exact output.

### Idle-stop on macOS

The `idle_watch.go::setDetachedAttrs` uses `Setsid` (a POSIX primitive, not Linux-only per Go's syscall docs). Validate on macOS:

```bash
# Setup: set idle_stop_minutes to a small value for the test
# In config.toml under [daemon]: idle_stop_minutes = 1

# Run a ct, let it finish
ct pi "echo hi"
# Wait > 1 minute
sleep 75

# Check: is the daemon stopped?
ct sys daemon status
# Expect: "Status: Stopped" and "Live sessions: 0 (idle-watcher armed if ...)"
```

The watcher should have stopped the daemon. If the daemon is still running after 75 seconds, the `Setsid` claim on macOS is wrong — file it as a critical macOS-specific bug.

If the daemon IS stopped, restart it for normal use:

```bash
ct sys daemon start
# Restore idle_stop_minutes to 45
```

### Idle-watcher log format

The watcher writes one line per decision to stderr:

```bash
grep "idle-watch:" ~/.config/construct-cli/logs/*.log
# Expect: "⏳ idle-watch armed: 1 minutes; checking every 30s" (or 45)
#          "idle-watch: a session appeared, standing down" (if a ct ran during the wait)
#          "💤 idle-watch: zero sessions past the deadline; stopping the daemon"
```

If you see anything else (errors, panics), capture the exact line.

### Skills daemon-recreate parity

When you change a skills-related config key (toggle `mount_skills`, flip `skills_read_only`, add a new entry to `~/Dev/EstebanForge/AGENTS/skills/`), the running daemon should recreate. Verify:

```bash
# Before
ct sys daemon status   # record the daemon's "started" time (or uptime)

# Toggle something
sed -i 's/skills_read_only = true/skills_read_only = false/' \
  ~/.config/construct-cli/config.toml
ct pi "echo hi"        # this should trigger a recreate

# After
ct sys daemon status   # daemon's "started" time should be RECENT (post-toggle)
# The msb-boot: line should be outcome=recreate with reason
# "host skills mounts changed (source, mode, or targets)"
grep "msb-boot:" ~/.config/construct-cli/logs/*.log | tail -1
```

If the daemon does NOT recreate, the skills hash label is not being checked (regression on the round-6 fix). File it.

## What to commit when you have data

1. Section 10 medians + dogfood frequency in `docs/VMsv2.md`. Format the medians as plain seconds (e.g. `Median cold boot (create + installs): 47`).
2. Any pass/fail data for P2.8 / P4.3 / P7.6 in the relevant phase section of `VMsv2.md` (each phase has a "what" + "how" captured above; the result slots in as a sub-bullet under the phase).
3. Any macro captures (concurrency issues, idle-stop macOS, doctor regressions) as separate issue notes. The agent will turn them into commits or tracked follow-ups.

Send the data dumps to the agent with a clear "P0 medians for 1.16.3: cold=47, recreate=12, warm=23, reconnect=0, recreate_count=4" style summary; the agent will do the bookkeeping commits and push.
