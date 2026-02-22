# Secret Redaction Mode Proposal (Opt-In)

## Status
- Draft proposal (revised)
- No runtime code changes in this document
- Implementation direction selected: hybrid model
  - Overlay/file redaction remains the workspace boundary
  - Run-only session authz + scoped proxy policy become the network/auth boundary

## Objective
Provide one clean, opt-in feature that prevents LLM agents running inside Construct from seeing raw secrets in mounted project files and environment variables.

Selected design direction for implementation:
- Keep Construct's overlay-based redaction architecture.
- Adopt an authy-like run-only session model for agent runtime credentials.
- Adopt an aivault-like provider pinning + policy-derived host model in Construct proxy.
- Add tamper-evident audit chaining for security-critical events.
- Add daemon isolation as a hardening phase, not a V1 blocker.

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

Security invariants that are not user-configurable in hide-secrets mode:
- No direct "get raw secret value" operation is available to the agent runtime.
- Provider traffic is allowed only through Construct proxy policy checks.
- Run-only session authz is enforced for all agent-originated secret-use flows.

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

### Selected External Patterns (Adopted)
This proposal intentionally adopts a hybrid of proven patterns:

1. **Workspace/file boundary (Construct-native):**
   - OverlayFS/APFS session view with redacted file copies.
2. **Run-only runtime authz (authy-like):**
   - Agent receives short-lived session credentials that allow secret use, never raw secret reads.
3. **Pinned provider policy boundary (aivault-like):**
   - Proxy uses provider pinning and policy-derived host/method/path decisions; caller input cannot override.
4. **Tamper-evident security audit (authy-like):**
   - Security events are chained with HMAC for integrity verification.

Why this hybrid:
- `agent-vault` and `psst` patterns are useful for UX/ergonomics, but their core boundaries are easier to bypass when protections are optional or workflow-dependent.
- Construct needs structural enforcement at runtime because agents execute arbitrary commands and network requests inside the construct environment.

Patterns intentionally not used as primary boundary:
- Hook-only enforcement (can be bypassed if hooks are missing/disabled).
- Convention-based agent instructions without runtime policy enforcement.
- Any fallback that injects raw provider keys directly into agent env when proxy policy is unavailable.

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

### Agent Runtime AuthZ (Run-Only Session Model)
Agent processes in hide-secrets mode must run under a short-lived, run-only session authorization model.

1. **Session token issuance:**
   - Construct host mints an ephemeral token per agent session.
   - Suggested token format prefix: `construct_hs_v1.`
   - Token has strict TTL (default 60 minutes), scoped to `session_id`, and is revoked on session end.
2. **Capabilities:**
   - Token may invoke approved proxy actions only (provider request execution through policy checks).
   - Token cannot call any path that returns raw secret values.
   - Token cannot mutate secret storage, policy, or provider mappings.
3. **Validation:**
   - Store only token HMAC/hash server-side; never persist raw token in session artifacts.
   - Validate in constant time to reduce timing-leak risk.
4. **Failure posture:**
   - Expired/revoked/invalid token => fail closed and abort request.
   - No fallback to raw env/provider key injection.
5. **Agent env exposure:**
   - If token is injected into agent env, mark it as runtime credential and exclude from logs/reports.
   - Token must never be printed in diagnostics; redact as `CONSTRUCT_REDACTED_TOKEN`.

### Provider Key Allowlist and Pinning
Provider API keys required for agent function are handled through a **trusted proxy + provider pinning** model:

1. **V1 (required):** Construct host process holds raw provider keys. Agent communicates with providers through Construct proxy endpoint only.
2. **Implementation:** Inject `HTTP_PROXY` and `HTTPS_PROXY` env vars into the agent process pointing to the internal Construct host.
3. **Provider pinning registry:**
   - Maintain a built-in provider map (`provider_id -> allowed_hosts, auth_strategy, managed_header_names, secret_name_patterns`).
   - Secrets associated with known provider identities are pinned to that provider identity for hide-secrets use.
   - Pinned provider identity is immutable for the session; mismatch fails closed.
4. **Host derivation:**
   - Effective upstream host is derived from proxy policy and provider mapping, never from arbitrary caller URL.
   - Caller-supplied full URLs to provider endpoints are rejected in hide-secrets mode.
5. **Hard rule:** If a provider is not supported by the proxy/pinning registry, it is unavailable to the agent in hide-secrets mode. No exceptions.
6. **Enforcement:** Set `ALL_PROXY` to the same internal proxy endpoint; clear agent-supplied `NO_PROXY/no_proxy` unless explicitly whitelisted for internal Construct endpoints.
7. **Network guardrail:** deny direct provider egress from the agent container in hide-secrets mode; allow provider traffic only via trusted proxy.

### Proxy Policy Contract (Mandatory)
The proxy contract must be explicit and testable:

1. **Request sanitization:**
   - Reject caller-supplied auth-class headers (`Authorization`, `Proxy-Authorization`, provider-specific managed auth headers).
   - Reject caller attempts to set broker-managed query auth parameters.
2. **Policy checks before auth injection:**
   - Validate method/path/host against provider policy first.
   - Inject auth only after policy pass.
3. **Redirect handling:**
   - Default mode: `block`.
   - Optional strict mode: `revalidate` each hop and strip auth on cross-host redirects.
4. **Response sanitization:**
   - Strip auth-class response headers/cookies before returning to agent.
   - Optional body blocklist redaction in hardening phase.
5. **Error redaction:**
   - Ensure URLs and diagnostics emitted by proxy never contain raw auth-bearing query/path/header fragments.

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
- security/session and proxy authz modules (new package paths to be finalized during implementation)

Key changes:
- Before starting agent session, prepare per-session overlay (upper layer with redacted files).
- Configure container to use OverlayFS merged view as workdir.
- Build masked env set for agent execution path (`run` and daemon `exec` paths).
- Mint and inject run-only session credential for proxy use.
- Route provider auth through trusted proxy with provider pinning/policy checks (no raw keys in agent env).
- Keep hide-secrets sessions non-persistent to project files (no write-back).
- Persist tamper-evident security audit events and verification metadata.

## Isolation Modes (Chosen Path)

### Mode A: In-Process Proxy Boundary (V1 Required)
- Proxy, token validation, and auth injection run in Construct host process.
- This is the minimum required runtime boundary for V1.
- Must still enforce run-only token + provider pinning + policy contract.

### Mode B: Daemon Proxy Boundary (Phase 2 Hardening)
- Move proxy/decryption/auth injection into a dedicated local daemon process.
- Agent-facing Construct process becomes a thin caller to daemon (local socket).
- Supports tighter OS-level isolation and future operator/agent user separation.

Daemon mode requirements when implemented:
1. Socket access permissions are restrictive by default.
2. Shared-operator mode can be enabled explicitly for multi-user setups.
3. If configured shared socket is expected but unavailable, fail closed (no insecure fallback).
4. Daemon and client protocol responses must never include raw secrets/tokens.

---

## Session Persistence Requirements

Persist enough data to make runs fast, deterministic, and debuggable.

Proposed root:
- `~/.config/construct-cli/security/`

Proposed files:
- `sessions/<session-id>/manifest.json`
- `sessions/<session-id>/redaction-index.json`
- `sessions/<session-id>/authz.json` (run-only session auth metadata, no raw token)
- `sessions/<session-id>/upper/` (overlay upper layer)
- `sessions/<session-id>/work/` (overlay workdir — required by OverlayFS)
- `sessions/<session-id>/errors.json` (only when failures occur)
- `audit/security-audit.log` (HMAC-chained JSONL events)
- `audit/security-audit.state` (latest chain head + format version)
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
- `authz_mode` (`run_only_token`)
- `session_token_id` (non-secret identifier, never raw token)
- `session_token_expires_at`
- `provider_capabilities` (allowed provider/capability list)
- `audit_chain_head` (latest event chain digest seen in-session)
- `policy_violations_count`

### `redaction-index.json` fields
- per file:
  - `path`
  - `source_hash`
  - `redacted_hash`
  - `redacted_ranges`: list of byte offset + length pairs (for diagnostics only, no raw secret values)

Important:
- Never persist raw secret values in session artifacts.
- Never persist raw session tokens in session artifacts.
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
- Session token leakage through logs/debug output could widen blast radius.
- Weak proxy host derivation could allow host-swap key exfiltration.

### V1 compatibility rules
1. Agent always runs against OverlayFS merged view.
2. Daemon works normally — overlay is transparent to the mount.
3. No project file write-back in hide-secrets mode (read-only session persistence model).
4. Keep feature opt-in and clearly documented.
5. Force `sandbox.mount_home=false` for effective runs while hide mode is active.
6. Do not auto-change `network.mode`; network policy remains independent.
7. Provider keys routed through trusted proxy — never in agent env.
8. Respect `hide_secrets_passthrough_vars` as explicit user-approved raw env passthrough.
9. Agent secret-use requests must carry valid run-only session authz.
10. Proxy host/method/path and auth injection decisions are policy-derived, not caller-derived.
11. Unsupported provider/pinning mismatches fail closed (no direct fallback).

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
- session authz mode + token id (id only, never raw token)
- proxy policy denials (count)
- false-positive candidates skipped (count, with `--verbose` for details)
- duration
- report file path
- persistence summary (changed files retained for review / no project write-back)

Never print raw values in logs.

### Tamper-Evident Security Audit
In addition to human-readable reporting, hide-secrets mode writes structured security events to an HMAC-chained audit log.

Event classes (minimum V1):
- `session.start` / `session.end`
- `token.mint` / `token.revoke` / `token.reject`
- `proxy.invoke.allow` / `proxy.invoke.deny`
- `proxy.policy.violation` (header/path/host/method/redirect categories)
- `redaction.summary`

Per-event fields (minimum):
- `ts`, `session_id`, `event`, `actor`, `outcome`
- `provider`/`capability` when proxy-related
- `details` (non-secret metadata only)
- `prev_hmac`, `chain_hmac`

Integrity model:
- `chain_hmac = HMAC(audit_key, prev_hmac || canonical_event_payload)`
- `audit_key` derived from host master key material using dedicated context label.
- Verification command: `construct sys security audit verify`.

Hard requirements:
- Audit failures do not leak raw secrets in error output.
- In hide-secrets mode, inability to append required audit events is fail-closed for security-critical operations (token mint, proxy allow).

---

## Rollout Plan

### Phase 0: Design and tests
- Add config schema and defaults (including `deny_paths`)
- Unit tests for masking behavior and detection engine
- Fixtures across env/json/yaml/toml/ini/properties/key files
- False-positive test suite (lock files, UUIDs, base64 data, SRI hashes)
- Add config comments warning about `hide_git_dir=false` risk
- Define run-only session token model (TTL, scope, revocation, deny semantics)
- Define provider pinning registry schema and policy contract
- Define HMAC audit chain format and verification behavior

### Phase 1: Functional V1
- OverlayFS session setup and teardown
- Redaction pass (path-first + content scan)
- Agent run path integration (overlay mount)
- Masked env injection with provider proxy
- Run-only session token mint/validate/revoke
- Provider pinning + host/method/path policy enforcement in proxy
- Auth-class header sanitization and redirect block behavior
- Read-only session persistence policy implementation (no project write-back)
- Session persistence and report
- `deny_paths` enforcement
- Tamper-evident security audit chain + verify command

### Phase 2: Hardening
- Detection coverage improvements based on real-world feedback
- Provider proxy hardening for all supported LLM providers
- Optional daemon boundary for proxy/decryption path (unix socket)
- Advanced proxy policy controls (rate limits, request/response size, response blocklist)
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
- Run-only token validation (TTL, revocation, constant-time compare behavior)
- Provider pinning resolution and mismatch failure
- Proxy request sanitization (auth header/query rejection)
- Audit chain append and verify logic

### Integration tests
- Mixed-language fixture project
- Agent sees masked values only
- Real project remains unchanged after hide-secrets session
- Session upper layer retains edits for manual review
- Daemon works under hide mode (overlay is transparent)
- Linux overlay mount lifecycle
- Zero-load regression when env gate is absent
- Agent proxy request succeeds only with valid run-only token
- Unsupported provider in hide mode fails closed with actionable error
- Redirect exfiltration attempts are blocked (or strictly revalidated by policy mode)
- Audit verify command passes on untouched log and fails on tampered log

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
- Verify caller-supplied auth-class headers are rejected by proxy
- Verify caller cannot override provider host with arbitrary URL
- Verify run-only session token cannot access secret-read/mutation operations
- Verify security-critical proxy allow events are chained in audit log

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

## Provider Policy Lifecycle

Approach:
- Ship a built-in provider policy registry with the binary for hide-secrets mode.
- Each entry defines:
  - `provider_id`
  - allowed host patterns
  - allowed auth injection strategy
  - managed auth header/query names
  - canonical secret name patterns (for pinning)
- Record `provider_registry_version` in session manifest and audit events.

Pinning semantics:
1. If a secret maps to a known provider identity, it is pinned to that identity for hide-secrets use.
2. Pinned identity cannot be changed by agent-originated operations.
3. Mismatched provider/capability resolution fails closed.

Why:
- Prevents host-swap exfiltration through arbitrary URLs or forged provider config.
- Makes behavior deterministic and auditable across versions.

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
- Run-only session authz token (TTL + revoke + fail-closed validation)
- Provider pinning registry with policy-derived host/method/path checks
- Mandatory proxy sanitization (auth header/query rejection, redirect guardrails, error redaction)
- Explicit raw env passthrough only via `hide_secrets_passthrough_vars`
- `.git` hidden by default (`security.hide_git_dir=true`)
- Fail-closed startup if overlay or redaction preparation fails
- Forced `mount_home=false` while hide mode is active
- Read-only session persistence model (no write-back to real project)
- `deny_paths` shipped in V1 for stricter control
- Tamper-evident HMAC-chained security audit with verification command
- Session cleanup when unchanged; changed sessions retained for manual review
- No session-level locking — per-session overlay provides full parallelism
- Daemon boundary intentionally deferred to hardening phase (keeps V1 tractable while preserving path to stronger isolation)

This gives a clean user experience and meaningful protection without pretending perfect detection or breaking existing default behavior.
