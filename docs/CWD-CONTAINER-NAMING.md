# Implementation Plan: CWD-Derived Container Naming

## Problem Statement

`internal/agent/engine.go:Execute()` hardcodes the direct container name as `"construct-cli"` (line 164). The running-state check at line 165 uses `docker ps --filter name=^construct-cli$` — an exact-match filter. Any second invocation from a different working directory hits the same name, finds the first container running, and falls into an interactive attach/restart prompt. The daemon path handles multi-CWD correctly via `MapDaemonWorkdirFromMounts`, but it requires explicit user configuration (`multi_paths_enabled = true` + `mount_paths`). Without that config, the direct container path is a hard singleton — one terminal at a time, regardless of working directory.

## Solution Overview

Replace `containerName := "construct-cli"` with a deterministic, CWD-derived name: `construct-cli-{8 hex chars}`, where the suffix is the first 4 bytes of `sha256(e.cwd)`. Same working directory always hashes to the same container name, preserving attach semantics for genuine same-directory re-entry. Different working directories produce different names and start independent containers with no conflict. Since `docker compose run` already passes `--rm` (engine.go:481), containers self-remove on exit — no accumulation. The daemon path (`"construct-cli-daemon"`, line 138) is untouched. All runtime functions in `internal/runtime/` already accept `containerName` as a parameter; no changes needed there.

---

## Changes Per File

### `internal/agent/engine.go`

Four changes, all in this one file.

---

**Change 1 — Add imports (lines 3–22)**

`engine.go` does not currently import `crypto/sha256` or `encoding/hex`. Both are already used in `runner.go` (same package), so no new module dependency.

```go
// Before
import (
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    stdruntime "runtime"
    "slices"
    "strings"
    "time"
    ...
)

// After — insert two lines in the stdlib block
import (
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    stdruntime "runtime"
    "slices"
    "strings"
    "time"
    ...
)
```

---

**Change 2 — Line 164: replace hardcoded container name**

```go
// Before
containerName := "construct-cli"

// After
containerName := cwdContainerName(e.cwd)
```

`e.cwd` is set in `Prepare()` at line 75: `e.cwd = sec.ProjectRoot()`. That resolves to `os.Getwd()` in normal mode and the hide-secrets overlay path in secure mode. `Prepare()` returns an error if `os.Getwd()` fails, so `e.cwd` is always non-empty before `Execute()` is called.

---

**Change 3 — Lines 169 and 182: update user-facing messages**

After the change, the container name is an opaque hash like `construct-cli-a3f7d2e1`. Showing it to the user is meaningless. Replace with the CWD.

```go
// Before (line 169)
fmt.Printf("⚠️  Container '%s' is already running.\n\n", containerName)

// After
fmt.Printf("⚠️  A container for '%s' is already running.\n\n", e.cwd)
```

```go
// Before (line 182)
fmt.Printf("🧹 Removing old stopped container '%s'...\n", containerName)

// After
fmt.Printf("🧹 Removing stopped container for '%s'...\n", e.cwd)
```

---

**Change 4 — Line 1001: add clarifying comment to test shim, do not change the string**

The package-level function `execInRunningContainer` at lines 988–1001 is documented as a "compatibility wrapper for existing unit tests" (comment at line 978). It bypasses `Prepare()` and manually constructs an engine with no `cwd` set. The test at `runner_test.go:505` asserts `gotContainer == "construct-cli"` against this shim specifically — that test remains correct and must not be broken.

```go
// Before (line 1001)
return e.execInRunningContainer(args, "construct-cli", providerEnv)

// After — comment only, string unchanged
// test shim: production code uses cwdContainerName() via Execute()
return e.execInRunningContainer(args, "construct-cli", providerEnv)
```

---

**Change 5 — Add `cwdContainerName` function (new, end of file)**

Append after `promptForAttachOrRestart` (currently the last function, ~line 622):

```go
func cwdContainerName(cwd string) string {
    sum := sha256.Sum256([]byte(cwd))
    return fmt.Sprintf("construct-cli-%s", hex.EncodeToString(sum[:4]))
}
```

---

### No other files require changes

| File | Verdict | Reason |
|---|---|---|
| `internal/runtime/runtime.go` | **Changed** | Now exports `CwdContainerName()` — the canonical implementation. Agent package delegates to it. Also exports `ExecNonInteractiveStream()` for `sys exec` command. |
| `internal/templates/docker-compose.yml` | No change | Service name is `construct-box`; container name is set dynamically via `--name` flag in `runNewContainer()` |
| `internal/agent/runner.go` | No change | `RunWithArgs` → `Execute()` → picks up CWD-derived name automatically |
| `cmd/construct/main.go` | **Changed** | Added `sys exec` command routing. `ct sys shell` calls `agent.RunWithArgs([]string{}, "")` which flows through `Execute()` |
| `internal/sys/ops.go` | No change | `sys update`, `sys doctor`, etc. bypass `Execute()` entirely; use `compose run` with no `--name` flag |
| All other `"construct-cli"` occurrences in `internal/` | No change | Every other match is a config directory path (`~/.config/construct-cli/`), not a container name |

---

## New Utility: `CwdContainerName`

**Location:** `internal/runtime/runtime.go`, package-level, exported.

**Signature:** `func CwdContainerName(cwd string) string`

The function was moved from `internal/agent/engine.go` (where it was unexported `cwdContainerName`) to the `runtime` package so it can be used by both the agent engine and `sys exec` command. The agent package delegates via `runtime.CwdContainerName(cwd)`.

**Algorithm:**
1. `sha256.Sum256([]byte(cwd))` → `[32]byte`
2. Take `sum[:4]` — first 4 bytes (32 bits of entropy)
3. `hex.EncodeToString(sum[:4])` → 8 lowercase hex characters
4. Return `"construct-cli-" + hexSuffix`

**Example outputs:**
```
/home/esteban/work/project-a  →  construct-cli-3d9e1f42
/home/esteban/work/project-b  →  construct-cli-c7a08b11
/home/esteban/work/project-a  →  construct-cli-3d9e1f42  (deterministic)
```

**Why 4 bytes:** A developer has at most a few hundred distinct project directories. Collision probability for N=100 directories over a 32-bit space is `N²/(2·2³²) ≈ 0.00012%` — negligible. Eight hex chars keeps the name short and debuggable.

**Edge cases:**

| Case | Behavior | Safe? |
|---|---|---|
| Empty string | Returns `construct-cli-e3b0c442` (SHA-256 of `""`), deterministic | Yes — `Prepare()` errors before `Execute()` if `os.Getwd()` fails; unreachable in production |
| Unicode in path (`/home/ñoño/proj`) | SHA-256 of UTF-8 bytes; output is always `[0-9a-f]` | Yes |
| Symlinked path | `os.Getwd()` returns the kernel's resolved path; consistent per session | Yes |
| `hide-secrets` mode | `sec.ProjectRoot()` returns the overlay dir; container hashes the overlay path, which is the active root | Correct |
| Trailing slash | `os.Getwd()` never returns trailing slashes on Linux/macOS | N/A |
| Hash collision | Two different paths produce the same 8-char suffix | Probability <0.01% for realistic workloads; see Open Questions if it ever surfaces |

---

## Testing Strategy

**Unit tests — add to `internal/agent/runner_test.go`:**

```go
func TestCwdContainerName(t *testing.T) {
    a := cwdContainerName("/home/user/project-a")

    // determinism
    if cwdContainerName("/home/user/project-a") != a {
        t.Fatal("same cwd must produce same name")
    }

    // differentiation
    b := cwdContainerName("/home/user/project-b")
    if a == b {
        t.Fatalf("different cwds produced same name: %q", a)
    }

    // format
    if !strings.HasPrefix(a, "construct-cli-") {
        t.Fatalf("missing prefix: %q", a)
    }
    if len(a) != len("construct-cli-")+8 {
        t.Fatalf("unexpected length %d: %q", len(a), a)
    }

    // no panic on empty
    _ = cwdContainerName("")
}
```

**Run unit tests:**
```bash
go test -run TestCwdContainerName ./internal/agent/...
make test   # full suite — must include runner_test.go:505 passing unchanged
```

**Manual integration tests:**

```bash
# Setup
mkdir -p /tmp/project-{a,b}

# Terminal 1 — start agent in project-a, leave it running
cd /tmp/project-a && ct sys shell

# Terminal 2 — start agent in project-b (must NOT block or prompt)
cd /tmp/project-b && ct sys shell

# Verify: two independent containers
docker ps --filter "name=construct-cli-" --format "{{.Names}}\t{{.Status}}"
# Expected: two different construct-cli-* entries, both "Up"
```

**Same-directory conflict still works correctly:**
```bash
# Terminal 1
cd /tmp/project-a && ct sys shell   # leave running

# Terminal 2 — same dir
cd /tmp/project-a && ct sys shell
# Expected: "A container for '/tmp/project-a' is already running." + attach/restart prompt
```

**Daemon path unaffected:**
```bash
# With daemon.auto_start = true in config
ct claude
docker ps --filter "name=construct-cli-daemon" --format "{{.Names}}"
# Expected: construct-cli-daemon present; no construct-cli-* containers
```

---

## Migration / Rollback

**Orphaned old containers:**

After deploying, any running `construct-cli` container (old naming) is invisible to the new code — the state check now looks for `cwdContainerName(e.cwd)`, not `"construct-cli"`. The old container runs to natural exit and self-removes via `--rm`. No user action required.

For users who want immediate cleanup:
```bash
docker stop construct-cli 2>/dev/null; docker rm construct-cli 2>/dev/null; true
```

**Rollback:**

Revert line 164 to `containerName := "construct-cli"` and drop the import additions. No persistent state, no file migrations, no database. Safe to roll back at any point without side effects.

---

## Open Questions

1. **`promptForAttachOrRestart` dead parameter.** Its signature is `func (e *RuntimeEngine) promptForAttachOrRestart(_ string) (string, error)` — the `containerName` argument is explicitly ignored. The caller at line 170 passes `containerName`, but the function never uses it. This was presumably intended for display but was never wired. After this change, the container name is an opaque hash anyway, so the parameter has even less value. Recommend removing the parameter in a follow-up cleanup commit; it's out of scope here.

2. **`ct sys doctor` container name filtering.** If `internal/sys/doctor.go` inspects or lists containers by filtering on `name=construct-cli`, it will miss CWD-derived containers after this change. Audit `doctor.go` before shipping — if it does a name-based filter, change the filter from exact `construct-cli` to prefix `construct-cli-`.

3. **Hash collision handling.** If two project paths ever collide on 4 bytes (probability <0.01%), they would see each other's containers as conflicts. Resolution is trivial: change `sum[:4]` to `sum[:6]` (12 hex chars) in `cwdContainerName`. The function is the single point of change.

4. **`hide-secrets` overlay path stability.** In secure mode, `e.cwd` is the merged overlay directory path, not the original project path. If the overlay path changes between invocations of the same project (e.g., it includes a timestamp or random component), the container name would change per session. Verify that `SecuritySessionManager.GetProjectRoot()` returns a stable, deterministic path for the same project before closing this ticket.

5. **Container name length limit.** Docker's documented limit is 63 characters for container names in some contexts. `construct-cli-{8 chars}` = 22 characters. Well clear of any limit.
