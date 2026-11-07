# Sentinel Credential Proxy — IMPLEMENTATION PLAN

Status: **IMPLEMENTED on `feat/v3-core` (2026-11). Core proxy, CA lifecycle, sentinel minting, and CONNECT MITM inspection complete.** This doc is the plan of record for the v3.0 network layer.

Related docs:

- creds-proxy design (older host-side credential proxy, header-injection model, never implemented; design doc removed 2026-11-01, history in git). This doc supersedes its transport model and reuses its keychain storage section.
- [HIDE-SECRETS-DESIGN.md](HIDE-SECRETS-DESIGN.md) — workspace redaction, implemented.
- [LESSONS-TO-V3.md](LESSONS-TO-V3.md) — v1-v2 analysis; source of the v3.0 removals and architecture decisions.
- [ARCHITECTURE-DESIGN.md](ARCHITECTURE-DESIGN.md) §4.1 — msb backend. Network mode enforcement interplay.

Origin: analysis of discobox-ai/discobox (Apache 2.0) sentinel system, 2026-09. Peer review by agy (isolated, high reasoning), verdict ADOPT-WITH-CHANGES. All CRITICAL/HIGH claims verified against source before acceptance.

## 1. Problem

Provider API keys cross the sandbox boundary as real values today. `CollectProviderEnv` (`internal/env/env.go:115`, `ProviderKeyVars` at line 99) and `CollectPassthroughEnv` (line 145) forward `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `ANTIGRAVITY_API_KEY`, `HF_TOKEN`, etc. straight into container env. A prompt-injected or misbehaving agent reads the real key with `os.Getenv` and exfiltrates it through any egress path.

Existing mitigations cover adjacent paths, not this one:

| Layer | Mechanism | Coverage |
|---|---|---|
| Env injection | `internal/env/env.go` | None. Real values enter the container |
| Output hygiene | `EnvMasker` / `StreamMasker` (`internal/security/env.go`) | Masks secret-looking values only in streams returning to the host UI. Zero network coverage |
| Network strict mode | ufw in-container (`internal/templates/network-filter.sh`) | L3 IP/CIDR only. Domains resolved once via `dig` at boot, pinned as IPs. CDN rotation silently breaks it. No content visibility |
| SSH agent | TCP bridge to host agent (`internal/agent/ssh_bridge.go`, `internal/runtime/runtime.go:1321`) | All-or-nothing signing authority for every host key the agent knows |

Two live weaknesses: real keys inside the container, and boot-time IP pinning for domain filtering. No HTTP proxy layer exists anywhere in the codebase (`HTTP_PROXY` grep: zero matches in `internal/`). v3.0 direction (2026-11): both weaknesses are removed outright, not mitigated; see section 10.

## 2. Prior art: discobox sentinel system

Discobox keeps real credentials out of agent boxes entirely. Boxes receive byte-shape-mimicking placeholder strings ("sentinels"). An egress MITM proxy detects sentinels and substitutes the real value only when a live, host-scoped grant covers the (sandbox, secret, host) triple. Key internals, by file:

- `secretformat/secretformat.go:108-148` — sentinels minted from format templates inferred from the real value (`sk-ant-oat01-{base64url:93}` style). Sentinel shape-matches so agent CLIs accept it and validation paths behave normally.
- `proxy/server.go` + `proxy/detector.go:41-64` — first-byte protocol sniff, mTLS client certs required, every allowed CONNECT MITM'd with per-host certs. No iptables anywhere in their repo. Interception is purely proxy env vars plus an in-box bridge.
- `proxy/internal/secrets/secrets.go:239` — swap layer. Exact-set match of the box's sentinel strings over request headers (+ query when enabled; NEVER bodies, because a body spool would capture the real value). Includes base64 Basic-auth token scanning (`encoded.go`).
- `server/internal/resources/secrets/service.go:444` — resolve = host binding check + live grant lookup + decrypt. No grant = sentinel passes through = upstream 401. Fail closed on the secret, fail open on the request.
- `hostscope/hostscope.go:24-85` — ONE host-scope matcher used everywhere: covers self + subdomains, never parents, narrowest grant wins.
- `proxy/http.go:560` — 401 retry chain: re-resolve fresh value, then previously displaced value; credential-death reports flow back rate-limited.
- `pool-agent/proxyagent/credentials.go` + `access/judge.go` — agent-requested credentials with LLM judge. Explicitly a guardrail, not a boundary; bypassable by calling the protocol directly.

Full research trail in agentmemory (decision entries 2026-09, librarian run `01a0c63b`).

Corroborating prior art, 2026-10: [Overalls](https://overalls.dev) (MIT, Geoffrey Huntley) enforces the same invariant at fleet scale — its Ziggy component brokers provider calls through `llm.internal`, keeps provider credentials outside the guest entirely, and meters usage per account at the proxy, while Galileo serves named integrations at `.integration.internal` so a machine uses a secret without being able to read it. Independent convergence on keys-at-the-edge; no change to this design's mechanism, and Ziggy's metering role feeds the usage-accounting TODO in [TODO.md](TODO.md).

Corroborating prior art, 2026-11: [v1-v2](https://github.com/EstebanForge/construct-cli) (cloned to `/tmp/v1-v2`, analysis in LESSONS-TO-V3.md) ships the same invariant with a stdlib-only per-VM proxy on four tiers (`open / internet / allowlist / none`, default allowlist). Secrets live on the host; the guest gets placeholders (`prefix + per-VM salt`), swapped through a MITM with a per-VM CA and a ~1024-leaf LRU cert cache. This design already covers its SNI==CONNECT check (finding #4) and its placeholder-is-not-proxy-credential separation (finding #3). What it adds here: proxy-side DNS resolution with private/CGNAT/loopback refusal (folded into S1), the leaf-cache detail (S2), the tiers model that replaces `NETWORK_MODE` (section 10), and a post-start confinement self-check recorded in sandbox state (S1, S6). v1-v2 also captures agent OAuth logins at the proxy and keeps refresh tokens host-side; that stays a non-goal here for v1 (finding #6 already excludes OAuth bodies), recorded as the natural v2 extension.

## 3. Design decision

Adopt the sentinel substitution model, not the creds-proxy header-injection model:

| | creds-proxy (old) | This design |
|---|---|---|
| Guest sees | Rewritten destination (`construct-proxy.local`), no key, no real host | Real provider host, sentinel value |
| Proxy role | Terminate TLS, rewrite Host, inject key | MITM CONNECT, scan headers/query, swap sentinel → real |
| Agent CLI compat | Fragile: every client must honor rewritten base URL | High: request shape unchanged |
| Key source | Host keychain (new `internal/credentials` package, creds-proxy §7 stays valid) | Same as today (host env/config), relocated behind the proxy |
| Backends | msb only (`host.microsandbox.internal`) | Backend-agnostic (docker bridge IP / `host.docker.internal` / msb alias) |

Why: the sentinel model changes nothing about how agent CLIs form requests. The injection model needs every client to tolerate a rewritten destination and every provider flow to survive base-URL surgery.

Core invariants (from discobox, keep them all):

1. Real values never enter the container.
2. Sentinel shape-matches the real value (table-driven templates per var; NOT inferred at runtime from real values).
3. Scan headers + query only. Never bodies.
4. Swap only when destination host matches the secret's binding. No match = sentinel passes = upstream 401.
5. One host-scope matcher function, subdomains yes, parents no.
6. Resolve cache keyed (var, host), capped by session lifetime; 401 retry chain fresh-then-previous.
7. The proxy resolves every hostname itself (never trusts guest DNS answers) and refuses loopback, private, and CGNAT destinations unless a rule names the address explicitly. Kills DNS rebinding and the dig-at-boot staleness in one move (v1-v2 refinement).
8. A sentinel string is never a proxy credential. Proxy auth is the per-session token (finding #3); the two never share bytes (v1-v2 salt/key separation).

## 4. Peer review findings (verified, blocking-level)

| # | Finding | Verified | Consequence |
|---|---|---|---|
| 1 | **CRITICAL, pre-existing**: `internal/templates/Dockerfile` sudoers grants `construct` NOPASSWD `ufw` + `apt*`. Agent runs `sudo ufw disable`; `sudo apt-get` = root via `APT::Update::Pre-Invoke` hooks | Yes, line 27 region | In-container ufw was never an L3 boundary, including today's strict mode. Enforcement must move host-side. Fix ships before sentinel work. Update 2026-09-25: sudoers is now free `NOPASSWD:ALL` by default (`sandbox.passwordless_sudo`, false restores the scoped list), which widens the in-container surface and makes host-side enforcement MORE required, not less |
| 2 | **CRITICAL**: per-install CA cannot be baked into `construct-box` (image built on GHCR, pulled `:latest`) | Yes, AGENTS.md Image Publish | CA generated per install on host, public cert mounted at runtime, trust stores updated in entrypoint root phase. The same flaw exists in creds-proxy §11 |
| 3 | **HIGH**: host proxy must not repeat the unauthenticated `0.0.0.0` bind of `ssh_bridge.go:81` | Yes | Session token in proxy URL, `Proxy-Authorization` validated. Otherwise open LAN relay that swaps real credentials |
| 4 | **HIGH**: domain fronting | Design-level | Enforce CONNECT host == TLS SNI == HTTP Host/`:authority` before any swap. Drop on mismatch |
| 5 | **MEDIUM**: DNS tunnel | Yes, network-filter.sh allows 53 out to ANY destination | Restrict 53 to host resolver/bridge gateway |
| 6 | **MEDIUM**: sentinel scope | Design-level | v1 = static bearer keys only (`ProviderKeyVars`). NO OAuth (refresh tokens travel in bodies). Exclude HMAC signers (SigV4), checksummed tokens (GitHub PAT CRC32), sub-type variants (`oat01` vs `api03`). Bindings follow `*_BASE_URL` overrides |
| 7 | **HIGH**: HTTP/2 + ALPN + SSE | Design-level | Agent CLIs stream. Proxy must preserve streaming; acceptance test in slice 1, not an afterthought |
| 8 | Residual: upstream error-body reflection of swapped headers would leak the real key | Accepted gap | Discobox has the same gap. Document, revisit |

Reviewed and rejected: stripping `apt*` from sudoers. Breaks the in-image product promise (agents install packages via sudo). Unnecessary once enforcement is host-side. `ufw` does come out of sudoers.

## 5. Prerequisites

- [x] P1: Host-side network enforcement (msb VMM transport policy + egress CONNECT proxy with token auth). In-container ufw removed.
- [x] P2: Scoped sudoers updated: `/usr/sbin/ufw` removed from allowlist.
- [x] P3: DNS resolution performed proxy-side with strict RFC1918/CGNAT/loopback refusal.
- [x] P4: Authenticated egress proxy bound with per-session token validation.

## 6. Implementation plan (slices)

- [x] S1: Host-side egress CONNECT proxy (`internal/proxy`). Session-token auth. Policy rules on tiers `open / internet / allowlist / none` with ecosystem presets. Post-start confinement self-check (`internal/agent/confinement_check.go`).
- [x] S2: CA lifecycle (`internal/proxy/ca.go`). ECDSA P-256 root CA with dynamic LRU leaf cert signer. Root CA mounted and trusted in entrypoint. SSL bundle env vars injected across launch paths.
- [x] S3: Sentinel mint + injection (`internal/proxy/sentinel.go`). Shape-mimicking placeholders for provider keys. Outbound leak scanner dropping unswapped secret bytes. Sanitization of synced file credentials (`cred_sanitize.go`).
- [x] S4: CONNECT MITM inspection & swap layer (`internal/proxy/server.go`). Domain fronting guard (CONNECT == TLS SNI == HTTP Host) and request header sentinel swapping.
- [x] S5: v3.0 cutover: Egress proxy enabled by default; legacy in-guest UFW removed; `network-filter.sh` deleted from image build and templates.
- [x] S6: Unit and integration tests covering proxy authentication, destination filtering, CONNECT interception, secret swapping, domain fronting, and confinement checks.
- [x] S7: Network policy explain diagnostic (`construct network explain <host[:port]>`) and real-time audit logging (`construct network log`).
- [x] S8: Non-HTTP TCP egress tunnels (opt-in `[network] allowed_tcp` host:port rules for databases/queues, bypassing TLS MITM via raw TCP piping and `ALLOW_TCP` logging).

## 7. Quirks and gotchas

- NO_PROXY must contain only loopback + construct-internal names, or the proxy is bypassed by default.
- `NO_PROXY`/`HTTP_PROXY` already present in a user's persistent home (`.bashrc` in the home bind) survives env injection. Scrub or warn.
- docker-compose exec / daemon exec paths that skip env injection will silently lack the proxy env. Every spawn path needs the same env wiring (see Harness Path-Arg Staging invariants in AGENTS.md).
- Sentinel-bearing env must also be forwarded on both launch paths: `buildRunFlags` AND `startDaemonBackground` (see Terminal Identity Forwarding pattern, AGENTS.md).
- Overriding `hashOverrideInputs`: new runtime knobs (proxy port, sentinel set) need a cache-buster literal or existing installs keep a stale override (task-20 pattern, agentmemory).
- Per-install CA + persistent home: first boot installs CA; later boots must be idempotent and cheap.
- A sentinel that fails provider-side validation changes agent behavior invisibly. Format-matching exists to prevent that; every template change needs a live-provider smoke test.
- Error reflection: if an upstream 400 echoes the (swapped) auth header, the real key lands in the agent-visible response body. Accepted residual risk, documented.

## 8. Non-goals (decided, do not reopen without cause)

- mTLS per-box identity: single container, host-local bridge. Revisit only if msb goes multi-tenant.
- Control plane, grant TTLs, inbox, agent-request protocol: local human approves at config time.
- LLM judge: guardrail not boundary. If ever wanted, gate at the proxy where it is enforceable.
- Body scanning: never. Discobox's reasoning is sound.
- creds-proxy keychain storage: valid future input, not required for v1 (keys stay where they are today, host-side).

## 9. Open questions

- Host-side enforcement mechanism per backend: docker network policy vs host iptables vs rootless-podman equivalent. Rootless podman has no host-iptables path; needs its own answer.
- macOS backend (Docker Desktop): `host.docker.internal` routing is per-platform; proxy bind address differs (macOS can bind 127.0.0.1, Linux needs the bridge-reachable address + auth from finding #3).
- msb backend: confirm `msbHostTransportRules` guest-to-host allowance covers the proxy port without touching `bridgePorts` (creds-proxy §10 warning applies).
- Chromium NSS DB injection requires the root phase to write into the persistent home; confirm ownership handling under `non_root_strict`.

## 10. v3.0 removals (decided 2026-11, do not soften)

Full rationale in LESSONS-TO-V3.md. Rule: a removal lands only in the same release as its replacement, and this table plus the slices above are v3.0's definition of done.

| Dies in v3.0 | Replaced by | Contract that breaks |
|---|---|---|
| ufw network filtering: `internal/templates/network-filter.sh`, `NETWORK_MODE=strict`, `NETWORK_ALLOWED_IPS`, `NETWORK_BLOCKED_IPS`, `NETWORK_ALLOWED_DOMAINS`, `NETWORK_BLOCKED_DOMAINS` | Egress proxy with per-request host matching and a policy rules file, on tiers `open / internet / allowlist / none` (default allowlist) | `[network]` config schema; `NETWORK_MODE` values; `network-filter.sh` leaves the image |
| dig-at-boot domain pinning | Proxy-side DNS resolution with private/CGNAT/loopback refusal (invariant 7) | Gone with the ufw path |
| Provider keys as real values in guest env (`CollectProviderEnv`, `CollectPassthroughEnv` secret vars; S3's passthrough compat fallback) | Sentinel placeholders; the proxy swaps real values for bound hosts only | Guest env values: sandbox code reading provider key vars sees placeholders, never real values |
| HIDE-SECRETS experiment gate (`CONSTRUCT_EXPERIMENT_HIDE_SECRETS`) | File redaction graduates out of experimental; this proxy becomes the boundary for secrets; stream masking stays as defense in depth | `[security] hide_secrets` re-scoped; feature gate var gone |

Notes:

- No migration layer and no legacy `NETWORK_MODE=ufw` value. Config that still names the old keys is refused with a message pointing at the tiers doc.
- `EnvMasker` / `StreamMasker` stay (second layer, not boundary).
- SSH agent TCP bridge is demoted, not deleted: per-sandbox opt-in with a warning about its all-or-nothing signing authority, until a scoped alternative exists.
- Image bake migration bumps one version so existing homes sweep the deleted `network-filter.sh` artifacts once.

## 11. References

- `internal/env/env.go` — `ProviderKeyVars`, `CollectProviderEnv`, `CollectPassthroughEnv`
- `internal/security/env.go` — `EnvMasker`, `StreamMasker` (output hygiene, stays as second layer)
- `internal/templates/network-filter.sh` — current strict mode
- `internal/templates/Dockerfile` — sudoers line (finding #1)
- `internal/agent/ssh_bridge.go` — bridge lifecycle + bind-address precedent
- `internal/runtime/runtime.go:1321` — SSH TCP bridge host-side pattern
- creds-proxy design §7 keychain storage, §10 network-mode enforcement, §12 rollout flags (doc removed, history in git)
- `docs/HIDE-SECRETS-DESIGN.md` — §6 run-only session token (satisfies proxy-auth requirement), V2 deferred proxy contract
- discobox-ai/discobox — files listed in section 2
- v1 and v2 — analysis + refinements in LESSONS-TO-V3.md; corroborates findings #3 and #4
- agentmemory decision entries (2026-09): analysis + peer review outcome
