# Credential Proxy: Design

Status: design only. Implementation is a separate effort after peer review.

Owner: Esteban. Phase 5 of [VMsv2.md](VMsv2.md).

## 1. Goal

Provider API keys (Anthropic, OpenAI, Google, etc.) stop crossing the microVM boundary. Today they enter the guest as `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GOOGLE_API_KEY`, etc. — agent code can read them via env, write them to logs, or exfiltrate them. The proxy keeps the keys on the host and brokers authenticated requests on the agent's behalf.

## 2. Why the proxy only works with TLS termination

The current bridge pattern is a blind TCP relay (e.g. `socat` in `internal/templates/entrypoint.sh`). It can forward bytes between guest and host, but it cannot:

- Inject an `Authorization` header into a request that does not already carry one
- Inspect the request URL or path to decide which key to attach
- Verify that the request is going to a provider (vs. an arbitrary host)

A provider-API call from the guest today looks like:

```
GET https://api.anthropic.com/v1/messages HTTP/1.1
Host: api.anthropic.com
anthropic-version: 2023-06-01
x-api-key: <key in env>
```

The key is already in the request; the proxy would be theatre. The right shape is:

```
GET https://construct-proxy.local/v1/messages HTTP/1.1
Host: construct-proxy.local
anthropic-version: 2023-06-01
```

…and the proxy rewrites the destination, attaches the real key, and forwards. The agent thinks it talks to `api.anthropic.com`; the proxy terminates TLS, rewrites, and opens a new TLS connection to the real provider. This requires:

1. A **construct-generated CA** baked into the image trust store (`/etc/ssl/certs/construct-ca.pem` or the platform equivalent). The guest only trusts this CA; the real provider CAs are not trusted from inside the guest.
2. A **host-side HTTPS proxy** that holds the provider keys and rewrites requests.
3. A **per-provider hostname allowlist** so the proxy refuses to forward to arbitrary hosts (the guest cannot reach `evil.com` even if it constructs a request to it).

## 3. Threat model

### In scope

- Agent code inside the microVM reading `os.Getenv("ANTHROPIC_API_KEY")` from the env. Mitigated by removing the env var entirely.
- Agent code writing the key to a log, a temp file, or a network request. Mitigated by the key never entering the guest; even with arbitrary file write, there is no key to exfiltrate.
- Agent code opening a raw TCP connection to bypass the proxy. Mitigated by msb egress policy: only the proxy port is allowed guest-to-host; the construct-box image has no other egress to the public internet.

### Out of scope (deferred to a follow-up design)

- Compromised construct CLI binary on the host. The proxy reads the key from the host keychain; a malicious binary could exfiltrate from there. Mitigation is outside the proxy: signed releases, host hardening.
- Compromised CA. If the construct CA leaks, an attacker can mint certs that the guest trusts. Mitigation: short CA validity, rotation, revocable bundle baked at image build.
- Side-channel via timing or traffic analysis. Provider APIs are not constant-time; an observer on the host can see request sizes. Out of scope for this design.

## 4. Component diagram

```
┌──────────────────────────────┐         ┌──────────────────────────┐
│ microVM guest                 │         │ host                     │
│                               │         │                          │
│  agent process                │  TLS    │  construct-credential-   │
│   │                            │ ──────>│  proxy                   │
│   │ https_proxy=http://...     │  443   │   │                      │
│   v                            │         │   │ rewrite + sign       │
│  libcurl / fetch               │         │   v                      │
│   │                            │         │  outbound HTTPS to      │
│   v                            │         │  api.anthropic.com      │
│  CAs: /etc/ssl/certs/          │         │      with real key      │
│       construct-ca.pem         │         │      attached            │
│       (ONLY this CA trusted)  │         │                          │
└──────────────────────────────┘         └──────────────────────────┘
```

Components:

- **construct-ca**: the X.509 root certificate the guest trusts. Generated on the host at first run (or rotated); baked into the construct-box image at build time via `Dockerfile`. Validity: 1 year, rotated on each major release.
- **construct-credential-proxy**: the host-side HTTPS proxy. Listens on a fixed port (e.g. `8443`) bound to the msb host alias `host.microsandbox.internal`. Holds the provider keys in the host keychain; signs each outgoing request with the right key based on the destination hostname.
- **Per-provider rule file** (`~/.config/construct-cli/credential-proxy.toml`): the policy table mapping hostnames to keys and provider behavior.
- **msb egress policy**: the network rules in `internal/runtime/backend_msb_run.go::msbHostTransportRules` extended to allow guest-to-host on the proxy port (already supported via the `bridgePorts` argument; the proxy runs on a known port added to that list).

## 5. CA lifecycle

1. **Generate** (host, first run): `openssl genrsa -out construct-ca.key 4096 && openssl req -x509 -new -nodes -key construct-ca.key -sha256 -days 365 -out construct-ca.pem -subj "/CN=construct-credential-proxy"`. Permissions: key 0600 owned by the construct user; cert world-readable.
2. **Embed** (image build): the cert is written to a known path in the construct-box image. The Dockerfile ensures `/etc/ssl/certs/construct-ca.pem` (Linux) or the platform equivalent (macOS keychain) trusts it. NO other CAs are added or removed; system CAs stay, but the proxy's hostnames resolve only to the construct CA.
3. **Sign** (host, on proxy start): the proxy holds the CA key and signs a short-lived leaf cert (`construct-proxy-cert.pem`, 24h validity) for the proxy's listener. The leaf is the cert the proxy presents to the guest. The CA signs the leaf; the guest only needs the CA.
4. **Rotate** (host, on version bump or operator request): regenerate the CA, re-embed in the next construct-box image, invalidate the old leaf. Old guests see cert errors after rotation; users re-pull the image.

The CA private key never enters the guest. The CA cert is the only thing the guest sees.

## 6. Per-provider rule format

The rule file at `~/.config/construct-cli/credential-proxy.toml` looks like:

```toml
# Default: deny. Each provider entry whitelists a hostname pattern and
# binds a keychain key to it.
[[provider]]
host_pattern = "api.anthropic.com"
keychain_key = "construct-anthropic-api-key"
inject_header = "x-api-key"
strip_request_auth = true   # remove any Authorization / x-api-key the guest sent

[[provider]]
host_pattern = "api.openai.com"
keychain_key = "construct-openai-api-key"
inject_header = "Authorization"
auth_value_prefix = "Bearer "
strip_request_auth = true

[[provider]]
host_pattern = "*.googleapis.com"
keychain_key = "construct-google-api-key"
inject_header = "x-goog-api-key"
strip_request_auth = true
```

Rules are matched in order; first match wins. `host_pattern` uses Go's path.Match syntax (limited glob, no regex). A request whose destination does not match any rule is dropped with a 403 (the guest sees a TLS error after the proxy closes; the agent interprets this as a network failure).

`strip_request_auth = true` is the security-critical flag: the proxy removes any `Authorization`, `x-api-key`, `x-goog-api-key` (and similar) header the guest sent, so a malicious agent cannot inject a header that points to a key it controls. The real key, fetched from the host keychain, is the only credential that ever reaches the wire.

## 7. Token storage

The proxy never receives a key on the CLI or in config. It reads from the host credential store via a small `internal/credentials` package:

- **macOS**: Security framework via `security find-generic-password -s construct-cli -a <keychain_key> -w`. Wrapper exposed as a `Keyring.Get(key) (string, error)` method.
- **Linux**: Secret Service via `secret-tool lookup service construct-cli account <keychain_key>`. Falls back to a permissions-protected file at `~/.config/construct-cli/keys/<keychain_key>` (mode 0600, owner-only) when Secret Service is unavailable (headless server, no dbus).
- **CI / ephemeral hosts**: the file fallback is the only path. The `ct sys creds set <key> <value>` command writes the file. The file is gitignored by default.

`Keyring.Get` is the only access path. The proxy never caches the key beyond a single request; long-lived caching is forbidden because the OS keychain is the source of truth and rotation must take effect immediately.

## 8. Guest wiring

Inside the microVM, the agent's HTTPS client (libcurl, requests, fetch) is configured via the standard `HTTPS_PROXY` env:

```
HTTPS_PROXY=http://host.microsandbox.internal:8443
HTTP_PROXY=http://host.microsandbox.internal:8443
NO_PROXY=construct-cli.local,localhost
```

The proxy is the only egress path. Other hostnames resolve to a TLS error from the proxy (no rule matched). The proxy listens on `host.microsandbox.internal` because msb's `msbHostAlias` already maps that to the host; the network policy in `msbHostTransportRules` already allows guest-to-host transport.

The `NO_PROXY` list covers the local construct API (in-container) so internal traffic does not round-trip through the proxy. The proxy is the only path to the public internet.

## 9. Guest env removal plan (the precondition)

The proxy only pays off if provider keys STOP being passed as guest env. Today, `collectForwardedEnv` in `internal/agent/engine.go` and friends copies `ANTHROPIC_API_KEY` (and others) from the host env into the guest env. The migration:

1. **Phase 1 (this design, no behavior change)**: design + impl. The proxy is built and runnable, but the env-passthrough is unchanged. Users can opt in by setting `HTTPS_PROXY` and testing.
2. **Phase 2 (default off)**: when `runtime.credential_proxy = true` (new config key, default `false`), the proxy is enabled AND the env-passthrough for provider keys is removed. Both flags must flip together; the config key encodes the contract.
3. **Phase 3 (default on, after one dogfood release)**: `runtime.credential_proxy` defaults to `true`. Provider keys are no longer in the guest env by default.
4. **Phase 4 (drop the legacy path)**: when 100% of dogfood users are on default-on, remove the env-passthrough code entirely. The proxy becomes the only path.

The rollout flag (`runtime.credential_proxy`) lets users opt out per-machine. A `ct sys creds doctor` command will report which phase the host is on and whether the env passthrough is still firing.

## 10. Network mode interaction

`internal/runtime/backend_msb_run.go::msbHostTransportRules` already permits guest-to-host transport. The proxy runs on a fixed port (e.g. `8443`) that is added to the `bridgePorts` argument of `BuildMsbRunSpec`. Concrete rules per mode:

- **permissive** (default-allow egress): no change needed. The proxy is reachable; all other hostnames resolve to the proxy, which drops them.
- **strict** (allowlist egress): the proxy port is added to the allowlist. Provider hostnames resolve to the proxy; the proxy mediates.
- **offline** (no egress): the proxy is still reachable, but the proxy itself cannot reach providers (no upstream network). A request returns a clear "no upstream network" error to the agent. Useful for offline sandboxes that still want local reasoning.

## 11. Image build

The Dockerfile (`internal/templates/Dockerfile`) needs:

```dockerfile
# Construct credential proxy CA: shipped with the image so the guest trusts
# the proxy. The CA itself is short-lived (rotated per release); the cert
# path is stable.
COPY construct-ca.pem /etc/ssl/certs/construct-ca.pem
RUN update-ca-certificates
```

The CA is regenerated by `construct build` and copied in at image build time. Hosts running the proxy ship the matching CA in their keychain; mismatched CAs produce TLS errors that are easy to diagnose.

## 12. Rollout flags (summary)

| Config key | Default | Effect |
|---|---|---|
| `runtime.credential_proxy` | `false` (Phase 1-2), `true` (Phase 3+) | Enables the host-side proxy AND removes the env passthrough for provider keys when `true`. |
| `runtime.credential_proxy_allow_insecure_fallback` | `false` | When `true`, the env passthrough remains active alongside the proxy. ONLY for emergency rollback; logs a warning on every run. |

The two flags are evaluated together: `credential_proxy = true && allow_insecure_fallback = false` is the only secure state. Any other combination is logged as a warning and the user is told to upgrade.

## 13. Open questions

- **macOS keychain ACLs**: the Security framework requires the proxy binary to have a specific ACL on each keychain item. The first integration test on macOS will reveal the exact entitlements needed. Document the workaround (a per-user keychain) if entitlements are too restrictive.
- **Image rebuild on CA rotation**: every construct-box image build re-embeds the current CA. Old images keep working until the leaf cert expires (24h) but cannot fetch new leaves after that. A "CA freshness" check in the proxy refuses to serve leaves for outdated guests, prompting the user to rebuild.
- **Proxy metrics**: the proxy should log a one-line summary per request (host pattern, status, latency) so a user can see what the agent is up to. Aggregated metrics (counts) ship to the construct log dir. PII redaction is the user's responsibility (the proxy never logs Authorization headers, but the agent's request body is NOT inspected).

## 14. Test plan

- **Unit (host)**: rule matching, header injection, header stripping, keychain adapter with a mock backend, CA generation.
- **Integration (live msb + live macOS/Linux)**: end-to-end agent run; assert provider key never appears in guest env (`cat /proc/<pid>/environ | grep API_KEY`); assert requests succeed via the proxy; assert denied requests fail with a clear error.
- **Failure modes**: keychain locked, CA expired, proxy down, msb egress policy blocks the proxy port. Each must produce a clean error, not a panic or hang.

## 15. Files this design touches (future PRs)

- `internal/credentials/` — new package: `Keyring.Get(key)`, `Keyring.Set(key, value)`, keychain + file adapters.
- `internal/proxy/` — new package: the host-side HTTPS proxy binary, or a `cmd/construct-credproxy` subcommand.
- `internal/runtime/credential_proxy.go` — new file: env-var filtering, opt-in gating.
- `internal/templates/Dockerfile` — embed CA, run `update-ca-certificates`.
- `internal/templates/entrypoint.sh` — set `HTTPS_PROXY` and `NO_PROXY` when the proxy is enabled.
- `docs/SECURITY.md` — expand the "credential handling" section.
- `CHANGELOG.md` — record each phase as a separate entry.

## 16. Review

- P5.1 (this doc) shipped; no implementation in this pass.
- P5.2 (review round) deferred. The design is the contract for the next round of peer review; the implementation will follow a successful review of this doc. Until then, no code is written.
