# Secrets Protection Tooling Comparison

Date: 2026-02-22

This document compares four external tools with Construct's current secrets-hiding proposal in `docs/SECRETS-REDACTION-PROPOSAL.md`, then proposes concrete enhancements for a more stable, clean, and safe implementation.

## Scope and Snapshot

Analyzed repositories (snapshot commit used):

- `botiverse/agent-vault` at `8f0bacf22eaa82938e4a8158926b29d82e678321` (2026-02-19)
- `Michaelliv/psst` at `2a4615a3a3d96f0388fab80f0f57f0c9f874be4d` (2026-02-09)
- `eric8810/authy` at `2e1be6f648e8bf87df33d0f982f7c2555edc7ee1` (2026-02-20)
- `moldable-ai/aivault` at `c480e3d8b4f2be6c77960e0805b1f00c33e0acfb` (2026-02-18)

Construct baseline for comparison:

- Current state in this repo is a plan document, not an implemented runtime path yet.
- Baseline source: `docs/SECRETS-REDACTION-PROPOSAL.md`

## Matrix: External Tools vs Construct Plan

Legend:

- `Strong`: built-in and structurally enforced
- `Partial`: present but optional or bypassable
- `Planned`: in proposal, not implemented yet
- `No`: not present

| Dimension | Construct (current plan) | agent-vault | psst | authy | aivault |
|---|---|---|---|---|---|
| Implementation status | Planned | Implemented CLI | Implemented CLI | Implemented CLI/lib | Implemented CLI + broker runtime |
| Primary security boundary | Overlay redaction + masked env + trusted proxy | Secret-aware file read/write placeholders | Command wrapper env injection + masking | Scoped secret access with policies and tokens | Vault + policy-enforced capability broker |
| Agent can directly read secret values | Planned `No` | Partial (safe if agent only uses tool commands) | Partial (`psst get/export` can reveal) | Strong with `run_only` tokens/policies | Strong by design (invoke capabilities, no raw value API in normal flow) |
| File-level redaction | Planned strong (overlay upper redacted copies) | Strong for `agent-vault read` output | No (has scan/hook for leaks) | Partial (`resolve` templates, not repo-wide redaction) | No |
| Env-level protection | Planned masked env + passthrough allowlist | No | Partial (inject + output masking, but optional `--no-mask`) | Strong for `run`; restricted by scope | Strong (auth injected by broker, not caller env) |
| Run-only enforcement model | No explicit session/token model yet | Partial (TTY gating on sensitive commands) | Partial (optional Claude hooks block bad commands) | Strong (token + policy `run_only`) | Strong (capability invoke boundary + token scope) |
| Policy granularity | Planned file/env/proxy config | Key-level placeholder workflow | Names, envs, tags | Glob allow/deny scopes + run-only | Capability host/method/path + advanced policy |
| Session token model | No | No | No | Strong (HMAC token, TTL, revoke, constant-time validation) | Partial/Strong (short-lived proxy tokens inside broker path) |
| Audit integrity | Planned session report only | Minimal | Minimal | Strong (HMAC-chained audit log) | Partial (append-only JSONL, no chain proof) |
| At-rest crypto model | Not specified in proposal details | AES-256-GCM, local `vault.key` file | AES-GCM + keychain/`PSST_PASSWORD` | age + HKDF, constant-time token checks, zeroize | XChaCha20-Poly1305, KEK/DEK hierarchy, Argon2, zeroize |
| Tamper-evident ciphertext binding | No | No | No | Partial | Strong (AAD binds secret id/scope/pinned provider) |
| Provider host pinning | Planned via proxy allowlist | No | No | No | Strong (registry-pinned secrets + host policy) |
| Egress hardening | Planned (direct provider egress blocked) | No | No | No | Strong (host derived from policy, SSRF guardrails) |
| Process isolation option | Trusted host process only | No | No | No | Strong (unix-socket daemon boundary, optional) |
| Agent onboarding safeguards | Planned documentation only | Skill guidance | Strong onboarding + optional hooks | Agent guide + scope model | Capability-centric CLI UX |
| Failure posture | Planned fail-closed | Mixed | Mixed | Fail on policy/run-only violations | Fail on policy violations |

## What Each Tool Contributes

### 1) agent-vault

Strong ideas:

- Clear "safe vs sensitive command" split.
- TTY-required sensitive commands (`set`, `get --reveal`, `rm`, `import`) reduce accidental agent exfiltration.
- Redaction placeholders with deterministic markers, including unvaulted high-entropy detection.

Limits relative to Construct goals:

- Security model depends on agent behavior using the right commands (workflow boundary, not full runtime containment).
- No provider proxy pattern, no network egress policy.
- Encryption key stored as local file (`vault.key`), not keychain-backed by default.

Useful for Construct:

- Keep explicit command class semantics for any future secret-debug/admin commands.
- Keep deterministic placeholder fingerprints for operability and debugging.

### 2) psst

Strong ideas:

- Practical runtime flow: inject secrets only into subprocess env and mask command output.
- OS keychain integration with fallback path.
- Good onboarding workflow (`onboard`) and optional hooks for Claude.

Limits relative to Construct goals:

- `get` returns plaintext values directly.
- `--no-mask` exists and can reveal output.
- Security hardening is partly optional via hooks; not enforced by core model.

Useful for Construct:

- Add first-class onboarding helper for agents.
- Add post-action scanning mode for leaked secret values in changed files as a defense-in-depth layer.

### 3) authy

Strong ideas:

- Strong run-only model enforced both at token and policy level.
- Short-lived, revocable session tokens (HMAC, constant-time compare).
- Tamper-evident audit log (HMAC chain).
- Explicit threat model and security invariants.

Limits relative to Construct goals:

- No provider-broker host pinning model by default.
- Not designed around project-wide file redaction overlays.

Useful for Construct:

- Add scoped ephemeral session credentials for the agent runtime.
- Add tamper-evident audit chain for all sensitive operations.
- Keep deny-by-default access semantics for secret-revealing operations.

### 4) aivault

Strong ideas:

- Registry-pinned secrets and capability policies (host/method/path) are high-value for exfiltration resistance.
- Broker-owned auth injection and caller header sanitization.
- Advanced policy controls (rate limits, body limits, response blocklist).
- Strong crypto hygiene (XChaCha20-Poly1305, KEK/DEK, Argon2, AAD binding, zeroization).
- Optional daemon boundary improves separation between caller process and secret-use process.

Limits / caution:

- Audit is append-only but not cryptographically chained like authy.
- Docs distinguish between not shipping HTTP route daemon yet and still having a local unix-socket daemon for invoke path; this is fine but requires precise wording in our own docs to avoid ambiguity.

Useful for Construct:

- Provider registry pinning + policy-derived host selection should be adopted.
- AAD binding pattern should be used for tamper-evident secret metadata constraints.
- Advanced proxy policy controls are useful for hardening phase.

## Recommended Enhancements to Construct Plan

## P0 (add before implementation starts)

1. Add a run-only session credential model for agents.
- Mint short-lived per-session token from host.
- Token can call only approved proxy actions; no "get secret value" path.
- Include TTL + revoke-on-session-end + constant-time validation.
- Source inspiration: authy + aivault.

2. Add registry-pinned provider mapping and immutable pin enforcement.
- Canonical env secret names map to provider identity/hosts.
- In hide-secrets mode, provider auth is injected only if request capability matches pin.
- No insecure fallback to direct provider env vars.
- Source inspiration: aivault.

3. Add tamper-evident audit chain.
- Log session start/end, redaction decisions, proxy invocations, policy denials.
- HMAC-chain each record with derived audit key material.
- Add `construct sys security audit verify`.
- Source inspiration: authy.

4. Strengthen proxy sanitization contract.
- Reject caller-supplied auth-class headers.
- Strip auth-class response headers before returning to agent.
- Default redirect behavior: blocked or strict revalidation.
- Keep request URL redaction in all error surfaces.
- Source inspiration: aivault.

5. Add conformance tests for hard guarantees.
- No raw secrets in agent env, logs, `/proc/*/environ`, args, session artifacts.
- Direct provider egress blocked when hide mode enabled.
- Proxy paths enforce host/method/path constraints.

## P1 (hardening right after V1)

1. Add optional daemon boundary for proxy/decryption path.
- Local unix socket boundary on Linux/macOS.
- Support autostart in local mode, fail-closed in shared-operator mode.
- Keep in-process fallback behind explicit opt-out only.
- Source inspiration: aivault.

2. Add advanced per-provider policy controls.
- Rate limits, max request/response size, response body blocklist.
- Source inspiration: aivault.

3. Add explicit agent onboarding scaffolding.
- Generate agent guidance snippets for Codex/Claude use in hide-secrets mode.
- Include guardrails for known bad commands and explain expected workflow.
- Source inspiration: psst.

4. Add post-session leak scan by value.
- Scan session output files for known secret values and encoded forms.
- Fail/report if leakage detected.
- Source inspiration: psst hook idea (as defense-in-depth, not primary control).

## P2 (advanced/enterprise)

1. Policy bundles and organizational presets.
2. Signed and exportable audit artifacts.
3. Pluggable external key providers (keychain/KMS/HSM) if needed.

## Changes Suggested for `docs/SECRETS-REDACTION-PROPOSAL.md`

Add or revise these sections:

1. `AuthZ Model for Agent Runtime` (new)
- Run-only token, TTL, revocation, permitted operations.

2. `Provider Registry and Pinning` (new)
- Canonical provider secret names, immutable pin rules, host derivation.

3. `Proxy Sanitization Contract` (new)
- Request header rejection, response header stripping, redirect policy.

4. `Audit Integrity` (new)
- HMAC chain format, verification command, retention behavior.

5. `Isolation Modes` (expand current runtime section)
- In-process vs daemon boundary with explicit failure semantics.

6. `Conformance Tests` (expand security tests)
- Add token misuse, pin-bypass attempts, redirect exfil tests.

## Anti-Patterns to Avoid

- Do not rely on agent conventions alone for core guarantees.
- Do not ship optional-only protections as the main boundary.
- Do not allow any fallback path that reintroduces raw provider keys into agent-visible env.
- Do not let docs claim guarantees that are not covered by conformance tests.
