# Secret Redaction Mode Proposal (Opt-In)

## Status
- Draft proposal (revised)
- No runtime code changes in this document

## Objective
Provide one clean, opt-in feature that prevents LLM agents running inside Construct from seeing raw secrets in mounted project files and environment variables.

Target UX:
- Pre-release: env master gate + config switch
- Default is disabled
- Works across mixed-language repositories without per-language setup

---

## User-Facing Configuration

Add a new section in `config.toml`:

```toml
[security]
hide_secrets = false
# Ignored unless CONSTRUCT_EXPERIMENT_HIDE_SECRETS=1
hide_secrets_mask_style = "hash"   # hash | fixed
hide_secrets_deny_paths  = []      # globs always scanned regardless of heuristics
hide_secrets_passthrough_vars = [] # env vars to never mask (e.g. ["PUBLIC_API_URL"])
hide_secrets_report = true
hide_git_dir = true
```

Behavior when enabled:
- Agent sees redacted values, never raw values
- Default mask format: `CONSTRUCT_REDACTED_<H8>` where `H8` is stable hash prefix (no raw chars exposed)
- Optional fixed format: `CONSTRUCT_REDACTED`
- Applies to project content and agent-visible env vars
- `mount_home` is forced off for that run
- Each agent session gets its own isolated overlay — no cross-session locking

---

## Feature Gating and Bootstrap

Mandatory master gate (pre-release):
- `CONSTRUCT_EXPERIMENT_HIDE_SECRETS=1`

Secondary gate:
- `security.hide_secrets` in config (default `false`)

Enablement rule:
1. If `CONSTRUCT_EXPERIMENT_HIDE_SECRETS` is not set to `1`, hide-secrets is unavailable and `security.hide_secrets` is ignored.
2. If `CONSTRUCT_EXPERIMENT_HIDE_SECRETS=1`, then `security.hide_secrets=true` enables the feature for that run.
3. If `CONSTRUCT_EXPERIMENT_HIDE_SECRETS=1` and config is `false`, feature remains off.

Zero-load requirement when disabled:
- No detector/rule engine initialization
- No overlay creation
- No redaction cache/session metadata writes
- No daemon behavior changes
- No additional runtime hooks on normal paths

Operator visibility:
- Emit one startup line in debug/info logs:
  - `hide_secrets=off (reason=experiment_gate_disabled)` when env gate is missing or not `1`
  - `hide_secrets=on (source=config)` when env gate is `1` and config enables it
  - `hide_secrets=off (source=config)` when env gate is `1` and config disables it

Rationale:
- Makes rollout fully isolated until release-ready.
- Keeps existing user behavior unchanged on machines without the env gate.

---

## Security Model

### Threat Model
Protect against:
- Agent reading `.env` and language-specific config files with credentials
- Agent printing or transmitting secrets found in files/env
- Accidental leakage in logs and terminal output

Not fully solved by this feature alone:
- Secrets already committed to git history and requested explicitly by user outside agent path
- Malicious host-side tooling outside Construct
- Secrets embedded in binary/archive payloads (out of scope in V1; not scanned)

### Core Guarantee
When `CONSTRUCT_EXPERIMENT_HIDE_SECRETS=1` and `security.hide_secrets=true`, the agent process must not have direct access to raw project secrets by default.

---

## Architecture

### V1 Approach: OverlayFS per Session + Masked Env

Construct already runs agents inside Linux containers. OverlayFS is available, battle-tested, and eliminates the hardest problems in the original copy-based design (sync-back, writer locking, daemon bypass).

#### How it works

1. **Lower layer (read-only):** the real project directory, mounted read-only.
2. **Upper layer (per-session):** an empty tmpdir that captures all agent writes.
3. **Redaction pass:** before mounting, scan candidate files and write redacted copies into the upper layer. OverlayFS serves the upper version when it exists, the lower otherwise.
4. **Env masking:** inject masked env vars to agent process. Raw values stay in the trusted Construct host process only.

```
Real project (lower, ro)
  ├── src/          ← agent sees directly (no secrets)
  ├── .env          ← hidden by redacted copy in upper
  └── config.yaml   ← hidden by redacted copy in upper

Session upper (rw)
  ├── .env          ← redacted copy placed here before mount
  └── config.yaml   ← redacted copy placed here before mount

Merged view (agent sees)
  ├── src/          ← passthrough from lower
  ├── .env          ← redacted from upper
  └── config.yaml   ← redacted from upper
```

#### Why OverlayFS over workspace copy

| Concern | Copy approach | OverlayFS |
|---|---|---|
| Disk overhead | Hardlinks help but redacted files still duplicated per project | Upper layer contains only redacted files (~KBs) |
| Sync-back | Complex bidirectional sync needed | Agent writes land in upper; non-secret files need no sync |
| Writer locking | Required to prevent cross-session races | Per-session upper — zero contention |
| Daemon compatibility | Must be bypassed in V1 | Works naturally — overlay is just a mount |
| Edit conflict on redacted files | Byte-range overlap detection needed | V1 avoids this via no write-back |

#### Write Policy (V1: Read-Only Session)

Agent edits land in the per-session upper layer only. In hide-secrets mode, Construct does not write session file changes back to the real project.

1. **Files that were redacted at session start**: treat as read-only from a persistence perspective (never write back).
2. **Non-secret files**: also not auto-written back in V1 while hide-secrets mode is enabled.
3. **New files created by agent**: remain in session upper layer only (no automatic sync).

User notification requirement:
- On session end, if any file changed in upper layer, show a warning with:
  - count of changed files
  - path to session upper layer for manual review
  - explicit note: "No changes were written to the real project in hide-secrets mode."

This removes merge and race complexity in V1 and guarantees no automatic project mutations from a redacted session.

#### Failure Policy (mandatory)
- Fail closed.
- If overlay setup or redaction preparation fails, abort agent run.
- Show a concise error that includes:
  - reason
  - files that could not be redacted
  - path to detailed report/log

---

## Detection and Redaction Strategy

### Scan Pipeline

Two-pass design leveraging host-side tools for maximum performance:

1. **Path-first pass:** identify candidate files by path pattern.
2. **Content pass:** Use `rg` (ripgrep) with specialized regex patterns to quickly identify files containing secret-like structures. 
3. **Redaction pass:** Construct parses and redacts only the specific files identified by `rg`. Skip files that don't match any path heuristic or `rg` pattern unless they appear in `hide_secrets_deny_paths`.
4. **Binary/archive exclusion:** binary and archive files are excluded from scanning/redaction in V1 to avoid large-file overhead.

### `.gitignore` Interaction

Do not skip candidate files because they are git-ignored.
- Secret scanning is independent from git tracking status.
- A git-ignored file can still contain real credentials and must be scanned.
- Hard exclusions (never scanned): `.git`, `node_modules`, `.venv`, `venv`, `vendor`, `target`, `dist`, `build`, `.next`, `.nuxt`, `.turbo`, `.cache`.

### Path-Based Coverage (baseline)
- `.env`, `.env.*`
- `*.pem`, `*.key`, `*.p12`, `*.pfx`, `id_rsa`, `id_ed25519`
- `.npmrc`, `.pypirc`, `.netrc`, `.dockercfg`, `docker-compose*.yml`
- `*.tfvars`, `*.tfvars.json`
- `application*.yml`, `application*.yaml`, `application*.properties`
- `config*.json`, `config*.yaml`, `config*.yml`, `config*.toml`, `*.ini`
- `.aws/credentials`-like content when present in mounted project

### Content-Based Detection (baseline)
- Private key blocks (`BEGIN ... PRIVATE KEY`)
- `API_KEY`, `TOKEN`, `SECRET`, `PASSWORD`, `AUTH`, `DSN`, `CONNECTION_STRING`
- Common provider key patterns (GitHub `ghp_`, OpenAI `sk-`, Anthropic `sk-ant-`, AWS `AKIA`, GCP, Azure, Stripe `sk_live_`/`pk_live_`, Slack `xoxb-`, etc.)
- JWT-like patterns and bearer tokens
- High-entropy strings — **only** when adjacent to a secret-indicating key name

### False-Positive Mitigation

Known false-positive sources and how to handle them:
- **Base64 non-secret data** (logos, fixtures): only flag if key name indicates secret
- **Lock file hashes** (`package-lock.json`, `composer.lock`, `yarn.lock`): hard-exclude from content scanning
- **UUIDs**: not flagged unless key name indicates secret (e.g., `SECRET_ID=<uuid>`)
- **Build/content/SRI hashes**: excluded by key-name heuristic
- **Test fixtures with fake secrets**: rely on key-name heuristics and pattern tuning to reduce noise

User controls shipped in V1:
- `hide_secrets_deny_paths`: globs that are always scanned regardless of heuristics
- No file-level scan allowlist in V1 (preserves core guarantee)

### Redaction Rules
- Preserve structure and syntax of original file
- Replace value only, keep key names intact
- Use a unique, searchable placeholder format: `CONSTRUCT_REDACTED_<H8>`
- Never expose raw secret characters in default mode
- Example:
  - Input: `GITHUB_TOKEN=ghp_1234567890abcdef`
  - Output: `GITHUB_TOKEN=CONSTRUCT_REDACTED_a13f92bd`
  - Input: `API_KEY=abc123`
  - Output: `API_KEY=CONSTRUCT_REDACTED_6ca13d52`
- For multiline secrets/certs, keep header/footer and redact body

### Symlink Policy
- Follow only symlinks that resolve inside the project root after canonicalization.
- Do not follow absolute symlinks or symlinks escaping project root.
- Escaping/broken symlinks: replace with placeholder content in upper layer AND:
  - Log warning per occurrence: `symlink_blocked path=<link> target=<resolved> reason=escapes_project_root|broken`
  - Collect all blocked symlinks in `manifest.json` under `blocked_symlinks: [{path, target, reason}]`
  - Include count in session report: `symlinks blocked: N (see manifest for details)`

### `.git` Exposure Policy
- Config flag: `security.hide_git_dir = true` (default true).
- When true, `.git` is excluded from the overlay merged view (opaque whiteout in upper layer).
- If user sets false, warn clearly that git history, reflogs, and past commits may expose secrets.

### Binary and Archive Policy
- Do not scan or rewrite binary/archive files in V1.
- Binary/archive files pass through from lower layer as-is.
- Binary/archive content is out of detection scope in V1.
- Binaries are mounted read-only from the real project perspective because V1 has no write-back.

---

## Env Var Handling

### Agent-Visible Env Vars
All env vars injected into the agent process are masked using the same `CONSTRUCT_REDACTED_<H8>` format, with these exceptions:

- Vars listed in `security.hide_secrets_passthrough_vars` are intentionally passed through unmasked.
- This list is the only user-controlled way to disclose selected raw env values to the agent in hide-secrets mode.
- Validate entries as exact env var names; ignore invalid names with warning.

### Provider Key Allowlist
Provider API keys required for agent function (LLM calls, tool access) are handled through a **trusted proxy** model:

1. **V1 (required):** Construct host process holds raw provider keys. Agent communicates with providers through Construct's proxy endpoint.
2. **Implementation:** Inject `HTTP_PROXY` and `HTTPS_PROXY` env vars into the agent process pointing to the internal Construct host.
3. **Security:** The proxy intercepts provider requests, injects the necessary raw keys/tokens from the host's secure storage, and forwards the request. The agent never sees the raw keys in its environment or memory.
4. **Hard rule:** If a provider is not yet supported by the proxy, it is unavailable to the agent in hide-secrets mode. No exceptions. No insecure fallbacks.
5. **Enforcement:** Set `ALL_PROXY` to the same internal proxy endpoint; clear agent-supplied `NO_PROXY/no_proxy` unless explicitly whitelisted for internal Construct endpoints.
6. **Network guardrail:** deny direct provider egress from the agent container in hide-secrets mode; allow provider traffic only via the trusted proxy.

### Explicit Env Var Classification
```
MASKED:      All user-defined env vars, .env contents, secret-indicating vars
TRUSTED:     Host-only trusted transport values (provider keys, internal runtime)
PASSTHROUGH: PATH, HOME, TERM, LANG, LC_*, and items in 'hide_secrets_passthrough_vars'
```

---

## Runtime Integration Points in Construct

This proposal affects:
- `internal/agent/runner.go`
- `internal/env/env.go`
- `internal/runtime/runtime.go`
- configuration in `internal/config/config.go` and template `internal/templates/config.toml`

Key changes:
- Before starting agent session, prepare per-session overlay (upper layer with redacted files).
- Configure container to use OverlayFS merged view as workdir.
- Build masked env set for agent execution path (`run` and daemon `exec` paths).
- Route provider auth through trusted proxy (no raw keys in agent env).
- Keep hide-secrets sessions non-persistent to project files (no write-back).

---

## Session Persistence Requirements

Persist enough data to make runs fast, deterministic, and debuggable.

Proposed root:
- `~/.config/construct-cli/security/`

Proposed files:
- `sessions/<session-id>/manifest.json`
- `sessions/<session-id>/redaction-index.json`
- `sessions/<session-id>/upper/` (overlay upper layer)
- `sessions/<session-id>/work/` (overlay workdir — required by OverlayFS)
- `sessions/<session-id>/errors.json` (only when failures occur)
- `cache/rules-version.json`

### `manifest.json` fields
- `session_id`
- `created_at`
- `host_project_root`
- `overlay_upper_root`
- `source_snapshot_hash`
- `ruleset_version`
- `mask_style`
- `files_scanned`
- `files_redacted`
- `secrets_redacted`
- `mode` (`run` or `daemon`)
- `persistence_mode` (`read_only_session`)
- `changed_files_count`

### `redaction-index.json` fields
- per file:
  - `path`
  - `source_hash`
  - `redacted_hash`
  - `redacted_ranges`: list of byte offset + length pairs (for diagnostics only, no raw secret values)

Important:
- Never persist raw secret values in session artifacts.
- Store only metadata, counts, and hashes.

### Session Lifecycle
- **Default:** cleanup on session end only if session upper layer has no file changes.
- **Changed session:** if file changes exist in upper layer, retain session artifacts and print review path.
- **Debug mode:** `CONSTRUCT_HIDE_SECRETS_KEEP_SESSION=1` retains session artifacts for post-mortem inspection.
- **Orphan detection:** process-alive check, not time-based TTL.
  1. Each session writes its PID to `sessions/<session-id>/pid`.
  2. Orphan sweep checks:
     a. Is the PID still running? (`kill -0 <pid>` or `/proc/<pid>/status`)
     b. Does the process name match Construct? (prevents PID reuse false negatives)
  3. If both checks fail → session is orphaned → eligible for cleanup.
  4. Grace period: 60 seconds after process death detection before cleanup (handles brief restarts).
  5. Fallback safety net: sessions older than 24 hours with no active PID are cleaned regardless (catches zombie edge cases).
- No long-lived TTL — sessions should not persist raw workspace state on disk longer than necessary.

---

## Compatibility and Break-Risk Analysis

### Risks if implemented naively
- Provider auth failures if runtime keys are masked before network calls.
- User assumes edits persist when hide-secrets mode intentionally keeps changes in session upper layer only.
- Tooling that expects real `.env` values fails inside agent.

### V1 compatibility rules
1. Agent always runs against OverlayFS merged view.
2. Daemon works normally — overlay is transparent to the mount.
3. No project file write-back in hide-secrets mode (read-only session persistence model).
4. Keep feature opt-in and clearly documented.
5. Force `sandbox.mount_home=false` for effective runs while hide mode is active.
6. Do not auto-change `network.mode`; network policy remains independent.
7. Provider keys routed through trusted proxy — never in agent env.
8. Respect `hide_secrets_passthrough_vars` as explicit user-approved raw env passthrough.

---

## Performance Plan

### Goals
- Fast startup after first scan
- Minimal disk overhead (only redacted files in upper layer)
- Incremental updates on re-runs

### Techniques
- Path-first candidate filtering as first pass (independent from git tracking status)
- Hash-based incremental scan (skip unchanged candidate files)
- Only redacted files written to upper layer (non-secret files pass through from lower)
- Cache detection results by `source_hash + ruleset_version`
- Path-first then content — two-pass pipeline avoids scanning non-candidate files

### Cleanup
- Session artifacts cleaned on session end (default)
- Orphan sweep for crashed sessions (PID-alive check + 24-hour safety net)
- Manual cleanup: `construct sys security clean`
- Size cap for detection cache directory

---

## Logging and Reporting

When enabled, emit a short session report:
- files scanned
- files redacted (with path list)
- secrets redacted (count only)
- false-positive candidates skipped (count, with `--verbose` for details)
- duration
- report file path
- persistence summary (changed files retained for review / no project write-back)

Never print raw values in logs.

---

## Rollout Plan

### Phase 0: Design and tests
- Add config schema and defaults (including `deny_paths`)
- Unit tests for masking behavior and detection engine
- Fixtures across env/json/yaml/toml/ini/properties/key files
- False-positive test suite (lock files, UUIDs, base64 data, SRI hashes)
- Add config comments warning about `hide_git_dir=false` risk

### Phase 1: Functional V1
- OverlayFS session setup and teardown
- Redaction pass (path-first + content scan)
- Agent run path integration (overlay mount)
- Masked env injection with provider proxy
- Read-only session persistence policy implementation (no project write-back)
- Session persistence and report
- `deny_paths` enforcement

### Phase 2: Hardening
- Detection coverage improvements based on real-world feedback
- Provider proxy hardening for all supported LLM providers
- macOS fallback (no OverlayFS — use copy-on-write with APFS clones)

### Phase 3: Advanced features
- Organization-level policy presets
- Custom detection rule packs
- Audit log export

---

## Test Strategy

### Unit tests
- Value masking formatter
- Detector rules for known token families
- Parser-specific redaction (dotenv, json, yaml, toml, ini)
- Entropy detector false-positive guardrails
- `.gitignore` independence (git-ignored candidate files still scanned)
- Session persistence policy (no project write-back in hide-secrets mode)

### Integration tests
- Mixed-language fixture project
- Agent sees masked values only
- Real project remains unchanged after hide-secrets session
- Session upper layer retains edits for manual review
- Daemon works under hide mode (overlay is transparent)
- Linux overlay mount lifecycle
- Zero-load regression when env gate is absent

### Security tests
- Verify no raw secrets in:
  - container env listing
  - `/proc/*/environ` inside container
  - command invocation args
  - logs
  - session metadata files
- Verify `.git` hidden by default in merged view
- Verify `mount_home` is forced off in effective hide mode
- Verify provider keys not in agent-visible env
- Verify upper layer contains no raw secret values
- Verify direct provider egress is blocked when hide-secrets mode is active

---

## Rules Lifecycle (Versioning and Maintainability)

Approach:
- Ship a built-in versioned ruleset with the binary.
- Record `ruleset_version` in each session manifest.
- Cache keys include `ruleset_version` to avoid stale matches.
- On upgrade, old cache remains valid only for identical ruleset version; otherwise recompute incrementally.

Why:
- Simple operational model (no external dependency service).
- Reproducible behavior per binary version.
- Easy debugging because each session states exact ruleset version used.

---

## Concurrency Model

OverlayFS per-session design eliminates cross-session contention entirely:
- Each agent session gets its own upper layer directory.
- Multiple agents can run against the same project simultaneously.
- No writer locks needed — sessions are fully isolated.
- No write-back to real project in hide-secrets mode, so no merge lock is required.

---

## macOS Compatibility

OverlayFS is Linux-only. Supported macOS versions: **14 (Sonoma), 15 (Sequoia), 16**.

All supported versions use APFS by default — `cp -c` clones are available unconditionally.

1. **Clone strategy:** use `cp -c` (copy-on-write clones) for non-redacted files + regular copies for redacted files. Near-zero disk overhead.
2. **Non-APFS external volumes:** `cp -c` fails gracefully to a regular copy (macOS handles this natively — no extra code needed).
3. **Persistence:** identical policy — keep session edits in session artifacts only; no project write-back in hide-secrets mode.

The macOS path uses a session directory structure identical to Linux. Only the mount/link strategy differs.

---

## Recommendation

Proceed with V1 as:
- Two-level gating (`CONSTRUCT_EXPERIMENT_HIDE_SECRETS=1` + `security.hide_secrets=true`)
- OverlayFS per-session isolation (Linux), APFS clones (macOS)
- Redacted upper layer with path-first + content scan pipeline
- Masked agent env with trusted provider proxy (HTTP/HTTPS)
- Explicit raw env passthrough only via `hide_secrets_passthrough_vars`
- `.git` hidden by default (`security.hide_git_dir=true`)
- Fail-closed startup if overlay or redaction preparation fails
- Forced `mount_home=false` while hide mode is active
- Read-only session persistence model (no write-back to real project)
- `deny_paths` shipped in V1 for stricter control
- Session cleanup when unchanged; changed sessions retained for manual review
- No session-level locking — per-session overlay provides full parallelism

This gives a clean user experience and meaningful protection without pretending perfect detection or breaking existing default behavior.
