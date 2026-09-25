# Sentinel Credential Proxy — PENDING WORK

Status: **PENDING. Not implemented. Design and peer review are done; implementation has not started.** Do not start coding before completing the prerequisites in section 5. This doc is the plan of record.

Related docs:

- [CREDS-PROXY.md](CREDS-PROXY.md) — older host-side credential proxy design (VMsv2 phase 5). Header-injection model. Design only, never implemented. This doc supersedes its transport model and reuses its keychain storage section.
- [HIDE-SECRETS-DESIGN.md](HIDE-SECRETS-DESIGN.md) — workspace redaction, implemented. Its deferred V2 items (trusted proxy enforcement, proxy contract checks, run-only session token) are exactly the network layer described here.
- [VMsv2.md](VMsv2.md) — msb backend. Network mode enforcement interplay.

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

Two live weaknesses: real keys inside the container, and boot-time IP pinning for domain filtering. No HTTP proxy layer exists anywhere in the codebase (`HTTP_PROXY` grep: zero matches in `internal/`).

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

## 3. Design decision

Adopt the sentinel substitution model, not the CREDS-PROXY header-injection model:

| | CREDS-PROXY (old) | This design |
|---|---|---|
| Guest sees | Rewritten destination (`construct-proxy.local`), no key, no real host | Real provider host, sentinel value |
| Proxy role | Terminate TLS, rewrite Host, inject key | MITM CONNECT, scan headers/query, swap sentinel → real |
| Agent CLI compat | Fragile: every client must honor rewritten base URL | High: request shape unchanged |
| Key source | Host keychain (new `internal/credentials` package, CREDS-PROXY §7 stays valid) | Same as today (host env/config), relocated behind the proxy |
| Backends | msb only (`host.microsandbox.internal`) | Backend-agnostic (docker bridge IP / `host.docker.internal` / msb alias) |

Why: the sentinel model changes nothing about how agent CLIs form requests. The injection model needs every client to tolerate a rewritten destination and every provider flow to survive base-URL surgery.

Core invariants (from discobox, keep them all):

1. Real values never enter the container.
2. Sentinel shape-matches the real value (table-driven templates per var; NOT inferred at runtime from real values).
3. Scan headers + query only. Never bodies.
4. Swap only when destination host matches the secret's binding. No match = sentinel passes = upstream 401.
5. One host-scope matcher function, subdomains yes, parents no.
6. Resolve cache keyed (var, host), capped by session lifetime; 401 retry chain fresh-then-previous.

## 4. Peer review findings (verified, blocking-level)

| # | Finding | Verified | Consequence |
|---|---|---|---|
| 1 | **CRITICAL, pre-existing**: `internal/templates/Dockerfile` sudoers grants `construct` NOPASSWD `ufw` + `apt*`. Agent runs `sudo ufw disable`; `sudo apt-get` = root via `APT::Update::Pre-Invoke` hooks | Yes, line 27 region | In-container ufw was never an L3 boundary, including today's strict mode. Enforcement must move host-side. Fix ships before sentinel work. Update 2026-09-25: sudoers is now free `NOPASSWD:ALL` by default (`sandbox.passwordless_sudo`, false restores the scoped list), which widens the in-container surface and makes host-side enforcement MORE required, not less |
| 2 | **CRITICAL**: per-install CA cannot be baked into `construct-box` (image built on GHCR, pulled `:latest`) | Yes, AGENTS.md Image Publish | CA generated per install on host, public cert mounted at runtime, trust stores updated in entrypoint root phase. The same flaw exists in CREDS-PROXY §11 |
| 3 | **HIGH**: host proxy must not repeat the unauthenticated `0.0.0.0` bind of `ssh_bridge.go:81` | Yes | Session token in proxy URL, `Proxy-Authorization` validated. Otherwise open LAN relay that swaps real credentials |
| 4 | **HIGH**: domain fronting | Design-level | Enforce CONNECT host == TLS SNI == HTTP Host/`:authority` before any swap. Drop on mismatch |
| 5 | **MEDIUM**: DNS tunnel | Yes, network-filter.sh allows 53 out to ANY destination | Restrict 53 to host resolver/bridge gateway |
| 6 | **MEDIUM**: sentinel scope | Design-level | v1 = static bearer keys only (`ProviderKeyVars`). NO OAuth (refresh tokens travel in bodies). Exclude HMAC signers (SigV4), checksummed tokens (GitHub PAT CRC32), sub-type variants (`oat01` vs `api03`). Bindings follow `*_BASE_URL` overrides |
| 7 | **HIGH**: HTTP/2 + ALPN + SSE | Design-level | Agent CLIs stream. Proxy must preserve streaming; acceptance test in slice 1, not an afterthought |
| 8 | Residual: upstream error-body reflection of swapped headers would leak the real key | Accepted gap | Discobox has the same gap. Document, revisit |

Reviewed and rejected: stripping `apt*` from sudoers. Breaks the in-image product promise (agents install packages via sudo). Unnecessary once enforcement is host-side. `ufw` does come out of sudoers.

## 5. Prerequisites (do these FIRST, they stand alone)

- [ ] P1: Host-side network enforcement for strict mode (docker network / host iptables on the bridge, outside container namespace). In-container ufw demoted to defense-in-depth.
- [ ] P2: ~~Remove `/usr/sbin/ufw` from image sudoers.~~ Superseded 2026-09-25: sudoers is `NOPASSWD:ALL` by default, so scoping ufw out of the allowlist no longer restricts anything; the enforcement point is host-side (P1). The scoped-allowlist opt-out (`sandbox.passwordless_sudo = false`) still carries the old list minus nothing; revisit only if the knob gains per-binary scoping.
- [ ] P3: Restrict outbound 53 to host resolver in all modes.
- [ ] P4: Authenticate existing host bridges or at minimum document the LAN exposure (ssh_bridge, herdr, clipboard, host-exec, loopback relays — `internal/agent/engine.go:217,603,609`).

## 6. Implementation plan (slices)

- [ ] S1: Host-side egress CONNECT proxy. Per-session lifecycle (same pattern as the SSH TCP bridge, `runtime.go:1321`). Session-token auth. Domain allowlist enforced at CONNECT (replaces dig-at-boot pinning). Strict mode: ufw default-deny + allow only proxy egress + proxy does L7. Acceptance: SSE streaming provider session works end-to-end; `HTTP_PROXY`-ignoring binary is blocked by L3.
- [ ] S2: CA lifecycle. Generate at `construct init` (0600 key, host-side). Cert mounted into container at boot. Entrypoint root phase installs into system store + NSS DB (`~/.pki/nssdb` for Chromium) + sets `SSL_CERT_FILE`, `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE`, `GIT_SSL_CAINFO`. Known gap: rustls-with-webpki-roots binaries reject custom CAs unconditionally (document, do not solve).
- [ ] S3: Sentinel mint + injection. Table-driven templates per `ProviderKeyVars` entry. Format-mimicking placeholder. Config: `[sandbox] secrets` with per-var host bindings, fallback = today's passthrough for compat. Bindings resolve through `*_BASE_URL` overrides.
- [ ] S4: Swap layer. Headers + query scan, base64 Basic-auth handling, SNI/Host pinning check, resolve cache, 401 retry chain.
- [ ] S5: Rollout flag + env removal, per-provider list (reuse CREDS-PROXY §12 phasing shape: opt-in → default on → drop legacy env path).
- [ ] S6: Tests. No real key in `/proc/*/environ`, sentinel accepted by real agent CLIs against real providers, fronting attempt dropped, 401 retry, streaming intact.

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
- CREDS-PROXY keychain storage: valid future input, not required for v1 (keys stay where they are today, host-side).

## 9. Open questions

- Host-side enforcement mechanism per backend: docker network policy vs host iptables vs rootless-podman equivalent. Rootless podman has no host-iptables path; needs its own answer.
- macOS backend (Docker Desktop): `host.docker.internal` routing is per-platform; proxy bind address differs (macOS can bind 127.0.0.1, Linux needs the bridge-reachable address + auth from finding #3).
- msb backend: confirm `msbHostTransportRules` guest-to-host allowance covers the proxy port without touching `bridgePorts` (CREDS-PROXY §10 warning applies).
- Chromium NSS DB injection requires the root phase to write into the persistent home; confirm ownership handling under `non_root_strict`.

## 10. References

- `internal/env/env.go` — `ProviderKeyVars`, `CollectProviderEnv`, `CollectPassthroughEnv`
- `internal/security/env.go` — `EnvMasker`, `StreamMasker` (output hygiene, stays as second layer)
- `internal/templates/network-filter.sh` — current strict mode
- `internal/templates/Dockerfile` — sudoers line (finding #1)
- `internal/agent/ssh_bridge.go` — bridge lifecycle + bind-address precedent
- `internal/runtime/runtime.go:1321` — SSH TCP bridge host-side pattern
- `docs/CREDS-PROXY.md` — §7 keychain storage, §10 network-mode enforcement, §12 rollout flags
- `docs/HIDE-SECRETS-DESIGN.md` — §6 run-only session token (satisfies proxy-auth requirement), V2 deferred proxy contract
- discobox-ai/discobox — files listed in section 2
- agentmemory decision entries (2026-09): analysis + peer review outcome
