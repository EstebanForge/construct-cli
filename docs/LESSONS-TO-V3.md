# Lessons from v1 and v2: What to Adopt in Construct v3.0

Analysis and production lessons from Construct v1 and v2 (2026-11). Construct v3.0 enforces strict network isolation and secret containment using one microVM per project folder. This document defines the architectural decisions for Construct v3.0 based on v1/v2 production experience, design stress-testing, and peer review.

Breaking changes are approved for v3.0. Legacy mechanisms with replacements are deleted in the same release. No fallback compatibility layers.

Related documents:
- [TODO-SENTINEL-PROXY.md](TODO-SENTINEL-PROXY.md) — Plan of record for the sentinel proxy and network cutover.
- [HIDE-SECRETS-DESIGN.md](HIDE-SECRETS-DESIGN.md) — Workspace redaction.
- [ARCHITECTURE-DESIGN.md](ARCHITECTURE-DESIGN.md) §4.1 — msb backend and daemon lifecycle.

## Core v3.0 Architecture

1. **Backend Priority**:
   - Primary: Microsandbox (`msb`) KVM microVMs.
   - Secondary: Docker container engine for hosts without KVM support (marked degraded in self-check).

2. **Network Isolation & Policy**:
   - Host-enforced boundary. In-guest ufw is removed only after host-side enforcement passes self-check.
   - msb microVM: VMM policy sets `DefaultEgress: PolicyActionDeny`. Only egress to the host proxy port (TCP) and bridge ports (SSH, clipboard) is permitted. UDP, ICMP, and direct TCP are blocked.
   - Policy scoping: Network policy is per VM. Daemon carries `construct.daemon.network_hash` label to trigger recreate on policy change.
   - Egress tiers: Default tier is `allowlist` (not open public). Construct ships built-in ecosystem presets (npm, PyPI, crates.io, Go proxy, apt, GitHub release assets).
   - Non-HTTP TCP: Opt-in `[network] allowed_tcp` host:port rules via CONNECT tunnel for databases (`psql`, `redis`).
   - Diagnostic feedback: Proxy returns HTTP 403 with reason headers on blocked egress. `construct sys network explain <host[:port]>` and `construct sys network log` provide debugging visibility.

3. **Sentinel Credential Proxy**:
   - Runs on the host, bound per session with authentication.
   - Secret containment: Real keys remain on the host. Guest environment and credential files receive shape-mimicking sentinel placeholders.
   - Outbound leak scanner: Proxy inspects all outbound headers and query strings and drops requests containing real secret bytes (catches accidental `.env` leaks).
   - Strict IP validation: Proxy resolves DNS independently, validates all A/AAAA records, and rejects private, loopback, CGNAT, link-local (`169.254.0.0/16`), and IPv6-mapped addresses. Connects directly to the validated IP to prevent DNS rebinding.
   - Host relays: Configured loopback forward ports pass through directly without secret substitution. Default `host_loopback_ports` is empty (fail closed).
   - Domain fronting guard: Enforces CONNECT host == TLS SNI == HTTP Host header. Mismatches abort immediately.
   - Certificate pinning: Explicitly declared pinned hosts bypass MITM inspection via CONNECT tunnel without secret substitution.

4. **Repository and `.git` Protection**:
   - Workspace mode:
     - macOS: Lock `.git/hooks` and `.git/config` via `chflags uchg`.
     - Linux: Mount an empty read-only filesystem over `.git/hooks`, and mount `.git/config` read-only into the guest mount namespace. Prevent `.git` directory rename bypass.
     - Host git hardening (`hostgit`): Every host git command runs with `core.hooksPath=/dev/null`, `core.fsmonitor=false`, `core.sshCommand` unset, and disabled diff/textconv filters.
   - `--clone` mode:
     - Dedicated isolated clone mode for zero-trust runs without workspace bind mounts.
     - Working tree initialized with current host diff. Results returned strictly as a validated `git bundle` or review branch, avoiding hardlink clone vulnerabilities (`--shared`).

5. **Shared Read-Only Toolchain Store**:
   - Replace monolithic fat container image with a minimal base image plus modular toolchains.
   - Toolchain artifacts: Pre-built, checksummed OCI artifacts on GHCR unpacked to `~/.cache/construct/toolchains/<hash>` and bind-mounted read-only into `/opt/toolchains`.
   - Artifact verification: Toolchains pinned by content digest and cryptographic signature. Fallback microVM installs stage in temporary storage and hash before caching.
   - Project detection: Automatically detect project markers (`package.json`, `Cargo.toml`, `go.mod`, `pyproject.toml`) and mount the union of all matching toolchains at launch.

## Security Invariants

1. **Least Privilege**: Guest execution drops root privileges after initial boot setup.
2. **Zero Secret Ingress**: No real secret enters the guest via environment variables, seeded credential files (`auth.json`, `hosts.yml`), or bind mounts.
3. **Restricted Host Bridges**: `construct-host-exec` and `osascript` restricted to explicit allowlisted verbs.
4. **Read-Only Shared Caches**: Shared host caches (e.g. qmd GGUF models) mounted read-only to prevent guest-to-host corruption of binary parsers.
5. **Host-Side Verification**: Self-check measured and verified strictly from the host, never trusting guest-reported status.

## Removals in v3.0

| Removed Item | Replaced By | Breaking Impact |
|---|---|---|
| In-guest ufw (`network-filter.sh`, `NETWORK_MODE=strict`) | Host-enforced VMM/bridge network policy + Egress proxy | `[network]` config schema; `network-filter.sh` deleted from image |
| Real provider keys in guest env (`CollectProviderEnv`, `CollectPassthroughEnv`) | Sentinel placeholders swapped at proxy | Sandbox code reading environment variables sees placeholders |
| Boot-time `dig` domain pinning | Proxy-side dynamic DNS resolution | Eliminates stale IP caching and DNS rebinding |
| Monolithic all-in-one image | Slim base image + Shared read-only toolchains | Faster cold start, modular runtime mounting |
| `CONSTRUCT_EXPERIMENT_HIDE_SECRETS` gate | Permanent workspace redaction + Proxy edge boundary | Feature flag removed; redaction enabled by default |
| Unrestricted host bridges | Verb-allowlisted bridges and scoped SSH agent | Arbitrary host command execution removed |

## Implementation Priority

1. **Host-Side Self-Check & Policy Engine**:
   - Probe msb VMM policy from host; fail closed if policy cannot apply.
   - Host git wrapper (`internal/hostgit`) to harden host-side git executions.
2. **`.git` Hardening**:
   - Linux and macOS `.git/hooks` and `.git/config` read-only mounts and immutability flags.
3. **Host-Side Sentinel Proxy and Egress Policy** (`TODO-SENTINEL-PROXY.md` S1–S5):
   - Proxy-side DNS resolution, address blocklist, SNI validation, and leak scanner.
   - Built-in ecosystem allowlist presets (npm, PyPI, crates, Go, apt).
   - Once verified by self-check, remove in-guest ufw.
4. **Credential Inventory & Sanitization**:
   - Sentinelize seeded file credentials (`auth.json`, `hosts.yml`).
5. **Shared Toolchain Store and Project Detection**:
   - Digest-pinned GHCR toolchain distribution.
   - Project marker detection and union mount resolution.
6. **`--clone` Execution Mode**:
   - Ephemeral clone workflow returning branches via verified git bundles.


