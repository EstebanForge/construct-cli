# Lessons from v1-v2 — What to Adopt

Analysis of [v1 and v2](https://github.com/EstebanForge/construct-cli) (cloned to `/tmp/v1-v2`, 2026-11). v1-v2 is a Go CLI, ~68k LOC, that runs one Lima microVM per project folder with an agent harness inside. It bet everything on one deep vertical: network confinement and secret isolation. This doc records what construct should learn from it, what is already planned, and what to skip. Decision 2026-11: this lands as v3.0. Breaking changes are allowed, and designs with a better alternative are deleted, not deprecated or kept as fallbacks. No dual-stack compat period.

Related docs:

- [TODO-SENTINEL-PROXY.md](TODO-SENTINEL-PROXY.md) — plan of record for the sentinel credential proxy (discobox-derived, peer review done, not implemented). v1-v2 independently confirms that design and adds concrete refinements.
- [HIDE-SECRETS-DESIGN.md](HIDE-SECRETS-DESIGN.md) — workspace redaction, implemented. Its deferred V2 items are the network layer below.
- [ARCHITECTURE-DESIGN.md](ARCHITECTURE-DESIGN.md) §4.1 — msb backend, network mode enforcement.

## Verdict

v1-v2 is more solid than construct on exactly two threats: a repo injection exfiltrating provider keys, and the agent escaping through the network. Construct enforces network rules once at boot, inside the guest, and trusts the result. v1-v2 enforces per request, outside the guest, and re-verifies after every start. On those threats it wins. Everywhere else the projects trade: v1-v2's proxy concentrates all secrets in one host process (total-compromise blast radius), breaks cert-pinned clients and mTLS, and its own docs admit the self-check only catches honest mistakes. Construct keeps the broader runtime (two backends, msb snapshot/fork, daemon reuse, harness staging) and imports the vertical wins as features.

## Confirms the plan of record

The sentinel substitution model in TODO-SENTINEL-PROXY.md (placeholder in, real value swapped at the edge, host holds the secret) is the same invariant v1-v2 ships. Independent convergence from a second codebase is corroboration, not new work. v1-v2 contributes five refinements to fold into that design when it is implemented:

| # | Refinement | What v1-v2 does | Value for construct |
|---|---|---|---|
| 1 | Proxy-side DNS resolution | The proxy resolves every hostname itself and refuses loopback, private, and CGNAT addresses unless a rule names the address | Kills the `dig`-at-boot weakness the sentinel doc lists as live: no stale CDN IPs, no DNS rebinding. Highest-value item on this page |
| 2 | SNI pinning | On CONNECT tunnels, the ClientHello SNI must name the CONNECT host or the connection is dropped | A rule cannot be bypassed by tunnelling to an allowed host while speaking to another |
| 3 | Secret vs proxy credential separation | Per-VM random salt ends placeholders; a different random key is the proxy password. A leaked placeholder cannot authenticate to the proxy | Keeps one leaked string from becoming both exfil payload and replay credential |
| 4 | MITM leaf-cert cache | Per-VM CA, LRU cache of ~1024 leaf certs, cached per host | Makes interception cheap enough for every session, not only privileged ones |
| 5 | Staged adoption | v1-v2 grew interception last: allowlist proxy first, secrets second, login capture third | Same staging the sentinel doc should follow; no MITM until the allowlist proxy runs in production |

## v3.0 removals (replaced, not deprecated)

Each removal below has a better alternative landing in the same release. Nothing ships as a fallback pair; the old path is deleted.

| Dies in v3.0 | Replaced by | Contract that breaks |
|---|---|---|
| ufw network filtering: `internal/templates/network-filter.sh`, `NETWORK_MODE=strict`, `NETWORK_ALLOWED_IPS`, `NETWORK_BLOCKED_IPS`, `NETWORK_ALLOWED_DOMAINS`, `NETWORK_BLOCKED_DOMAINS` | Egress proxy with per-request host matching and a policy rules file, on tiers `open / internet / allowlist / none` (default allowlist) | `[network]` config schema; `NETWORK_MODE` values; `network-filter.sh` leaves the image |
| Provider keys as real values in guest env (`CollectProviderEnv`, `CollectPassthroughEnv` secret vars) | Sentinel placeholders; the proxy swaps in real values for bound hosts only | Guest env values: sandbox code reading provider key vars sees placeholders, never real values |
| dig-at-boot domain pinning | Proxy-side DNS resolution with private/CGNAT/loopback refusal (refinement 1) | Gone with the ufw path |
| HIDE-SECRETS experiment gate (`CONSTRUCT_EXPERIMENT_HIDE_SECRETS`) | File redaction graduates out of experimental; the proxy becomes the boundary for secrets; stream masking stays as defense in depth | `[security] hide_secrets` re-scoped; feature gate var gone |

Notes:

- No migration layer and no `NETWORK_MODE=ufw` legacy value. v3.0 config that still names the old keys is refused with a message pointing at the tiers doc.
- `EnvMasker` / `StreamMasker` stay: with placeholders in guest env, host-side streams carry placeholders, and the masker still catches values surfaced through file redaction and host reads.
- The SSH agent TCP bridge is demoted, not deleted: it stays the only path for git-over-ssh signing, but becomes per-sandbox opt-in with a warning about its all-or-nothing signing authority, until a scoped alternative exists.
- Image bake migration (stale bind-copy sweep) bumps one more version so existing homes sweep the deleted `network-filter.sh` artifacts once.

## New scope (not covered by any current doc)

### Confinement self-check

After every sandbox start, prove the network mode holds: a direct connection from the guest must fail, the proxy (once it exists) must answer, and the backend process must be the confined one. Record the result in the sandbox state as inspectable data (`ok` / `unenforced`), not a log line. Small, immediate, independent of the sentinel proxy. This is the single cheapest borrow on this page.

### .git guard and hardened host git

Threat construct ignores today: the workspace is bind-mounted read-write, so a planted `.git/hooks/*` or `core.fsmonitor` / `core.hooksPath` / `core.sshCommand` in `.git/config` executes on the host at the user's next host-side git command. v1-v2's answer: lock the paths so guest root cannot clear them (`chflags uchg` on macOS; Linux has no equivalent, so report and let the human decide, never auto-repair), and harden every host-side git invocation (no fsmonitor, no hooks, no filters, ssh-only transport, no `GIT_*` env inheritance, no prompts). Applies to both docker mounts and msb mounts.

### `--clone` mode

A run mode where the sandbox gets its own clone of the repo and returns work as a fetchable branch (`git fetch construct`), reviewed before merge. Review gate by construction, and it neutralizes the .git threat for that mode entirely. Natural successor to msb's snapshot/fork restore path.

### Backend conformance suite and reviewed-emulation gate

v1-v2 defines backend features in a capability matrix; an emulated feature is refused until a design note, a threat check, and e2e checks prove the guarantee. The same checks then run for every backend in one conformance harness. Construct's docker/msb behavioral parity lives today as prose warnings in the repo AGENTS.md (recreate labels, digest drift, mount hashing). A shared `backendtest`-style suite plus a formal gate for emulated features turns that lore into enforced invariants.

### Shared read-only toolchain store

Tool images built once, keyed, checksummed per platform in a lock file, mounted read-only into every sandbox. Two details worth keeping: daily re-check of upstream releases against the lock, and per-tool glibc measurement (`readelf` max `GLIBC_` version recorded per tool image) so tools match the base libc. Cuts image churn and cold-start time. Construct's qmd model cache (`~/.cache/qmd/models`) is the existing shared-cache precedent.

### Image lifecycle housekeeping

Build locks held in a file whose OS lock releases on crash (record missing plus lock free means unfinished: delete and rebuild); rebuild counters hashed into image keys so dependents invalidate; a `gc` command with retention settings for images, tool caches, and stopped sandboxes. Construct accumulates artifacts with no cleanup story.

### Project-file toolchain detection

Marker files at the project root select default parts at launch (v1-v2: `package.json` selects node, `Cargo.toml` selects rust, `default.nix` checked for `mkShell` vs package builds). For construct: pre-seed skills, agents, and packages per project instead of baking the union into one image. Detection is a fixed-order table so the same file mix always gives the same sandbox.

## Engineering discipline (test and code practice)

- **One atomic write path.** Linter rules (`forbidigo`) ban raw `os.WriteFile` / `os.Create` / `O_TRUNC` everywhere except one atomic write helper, and ban pid-signalling outside the daemon package. Invariants enforced by the linter, not by convention.
- **Stub binaries with golden output.** One stub per host command, dispatched on `argv[0]`, with golden files of real tool output per OS, and per-package coverage baselines checked in CI. This is how v1-v2 tests host integration without real hosts.
- **State as sections under one lock.** Per-sandbox state files with one section per package, every write under the sandbox lock, layout version plus migrations, and a test that lists every state file so format drift is visible in review.
- **Invariant prose style.** v1-v2's AGENTS.md states each part as "one reader, one writer, made once". Construct's AGENTS.md already carries the right content; adopting the per-part invariant phrasing makes reviews sharper.

## Skip, and why

| Item | Why skip |
|---|---|
| Lima/QEMU backend | msb snapshot/fork restore is a capability v1-v2 lacks; docker plus msb stays the bet |
| Per-project disk VMs for everything | Disk-hungry; the daemon reuse model fits yolo-agent workloads better |
| Full i18n machinery (generated message catalogs) | YAGNI for a single-user tool; keep plain strings |
| MITM everything at once | Still staged, but for engineering risk only, not compat: allowlist proxy first, then interception, then login capture. No legacy fallback survives alongside |
| Adopting their runtime | Same conclusion as the v3.0 decision: keep the tree, ship the lessons as features |

## Priority order

1. ~~Fold refinements 1-3 (proxy-side DNS, SNI pinning, salt/key separation) plus the removal table above into TODO-SENTINEL-PROXY.md.~~ DONE 2026-11: folded as invariants 7-8, slice updates (S1-S3, S5, S6), and section 10 in TODO-SENTINEL-PROXY.md, which is now the v3.0 network plan of record. SNI pinning needed no fold: finding #4 already had it.
2. Confinement self-check, recorded in sandbox state.
3. .git guard plus hardened host git preflight.
4. Backend conformance suite and the reviewed-emulation gate.
5. Shared read-only toolchain store.
6. `--clone` mode.
7. Atomic-write linter rules and stub-with-golden test harness.
8. Image gc and toolchain detection.

Sequencing rule for v3.0: a removal lands only in the same release as its replacement, and the removal list is the release's definition of done alongside the proxy itself.
