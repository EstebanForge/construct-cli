# Thin Sandbox Pivot: cwd-Containment Primitive

Status: PROPOSAL. Not shipped. No code changed by this document. Owner: Esteban. Last updated: 2026-10-11.

Scope: a candidate product redefinition. The core primitive shrinks from "give agents a separate computer" to "keep an agent inside the folder it was started in, with network limits and credential redaction". This document records the analysis, the threat model, the known breakage points, and the target shape. Nothing here is implemented; `docs/ARCHITECTURE-DESIGN.md` and `docs/VMsv2.md` remain the shipped design baseline until this proposal is approved.

Provenance: derived from the Microsoft Execution Containers (MXC) general-availability announcement (blogs.windows.com/windowsdeveloper, 2026-10-07), a full audit of the MXC source tree (Rust workspace, ~246k LOC, config schema stable at 1.0.0), and one adversarial peer-review round from an isolated reviewer session with read access to both codebases. The reviewer reached the same verdict independently.

## 1. The primitive

The user has an agent installed. The user picked it. Construct does not install, register, or provision anything.

The only job: start the agent so it can read and write inside the folder where it was initialized (and its subfolders), and nowhere else. Goodies on top: network limits, credential redaction.

Everything in the current product that does not serve that job is a different product. This includes the agent registry, the `construct-box` image, package provisioning, the persistent agent home, the daemon lifecycle, and the msb microVM backend. Each exists to solve "give the agent an environment". The primitive says the environment already exists: the user's machine.

## 2. What MXC is

MXC is a policy-driven containment library, not a product. Applications embed it (SDKs: Rust, .NET, Node) or call its executors (`lxc-exec`, `wxc-exec`) with a JSON request. It enforces a developer- or IT-authored boundary at runtime, independent of the contained workload.

Backends:

| Backend | Platforms | Status | Notes |
|---|---|---|---|
| Process container (AppContainer / Seatbelt / Bubblewrap) | Windows, macOS, Linux | Stable, default | Unprivileged user-space sandbox, shared host kernel |
| LXC | Linux | Stable | Full container, host kernel |
| WSL container | Windows 11 | Stable | |
| Windows Sandbox | Windows 11 | Stable | |
| IsolationSession | Windows 11 | Stable | Separate account, isolated desktop, clipboard, UI |
| Nanvix microVM | Windows, Linux | Experimental | Python3-only guest, ~100 MB, ~100 ms start; WHP warm snapshot on Windows, KVM cold boot each run on Linux |
| Hyperlight (Unikraft unikernel) | Windows, Linux | Experimental | Guest runs source runtimes, not arbitrary binaries |

Policy surface (schema 1.0.0): per-path filesystem `rw`/`ro`/`deny`, network egress/ingress rules with proxy support, UI/clipboard isolation. Lifecycle: provision, start, exec, stop, deprovision. Operating modes: enforcement, learning (deny and record), and an `--audit` mode that disables the sandbox and records accesses to author least-privilege policy.

## 3. Capability matrix

| Capability | MXC | construct (today) | Needed for the primitive |
|---|---|---|---|
| Containment primitive | 9 backends incl. hardware VM | docker compose + msb microVM | Process tier only |
| Per-path allow/deny policy | Yes | No (coarse root mounts) | Yes |
| Network egress policy | Per-rule, deny-by-default, proxy | `network-filter.sh` (coarse) | Yes |
| Environment provisioning | None | `construct-box`, 14 agents, packages | No |
| Agent registry / staging / memories | None | Yes | No |
| Mount learning UX | Audit artifacts | `roots_store` prompts | Mostly no |
| Persistent agent home | Manual lifecycle only | Design center | No |
| Host integration (clipboard, terminal identity, ssh bridge) | None | Yes | No |
| Windows | 5 backends | None | Deferred |
| Consumer | App developers embedding a library | End users running agents | End users |

## 4. Feature audit against the primitive

| Construct feature | Serves the primitive? | Reason |
|---|---|---|
| `construct-box` image, 14 baked agents, `packages.toml` | No | Agent already on the host |
| Agent registry, runner gating, memories | No | User already chose the agent |
| Daemon flock, idle watch, session registry | No | No daemon in the thin model |
| Mount learning, `roots_store` | Mostly no | One bind of cwd plus a deny list |
| Path-arg staging, skills mounts | Trivial | Just bind them |
| msb microVM backend | Different threat tier | See section 5 |

Cost accounting: the thin tier is a few hundred lines of Go per OS (a Bubblewrap argv builder, a Seatbelt profile builder). The current runtime is tens of thousands of lines plus an image build and publish pipeline. The provisioning machinery paid for a problem the primitive deletes.

## 5. Threat model

Honest boundaries. A process sandbox defeats the failures that actually happen: model error, prompt injection, hostile repo content instructing the agent to read `~/.aws`, deletes outside the project, exfiltration to a random host. It shares the host kernel, so it does NOT defeat:

1. A hostile repo running a real kernel exploit chain. Only a VM boundary survives. This is the one thing the msb tier buys, and the reason the heavy tier stays available opt-in.
2. Exfiltration through an allowed destination. If `api.anthropic.com` is allowed, secrets can leave inside a legitimate request. Fix: redaction and scoped tokens, not sandboxing.
3. Persistence inside the project. The agent plants `.git/hooks` or a `package.json` postinstall; the user's own unsandboxed shell runs it later. Mitigation: deny write to `.git/hooks` and similar files. No sandbox can fully fix this.

## 6. Known breakage points and fixes

Real machines break naive "cwd-only" sandboxes. These are the known cases and the standard fixes (several validated against MXC's per-backend characterization tests):

1. Toolchains in `$HOME` (`~/.cargo`, mise, nvm, `~/.rustup`): bind read-only. Writable caches (`~/.cache`, `~/.npm`, `~/.cargo/registry`) get tmpfs or a narrow rw allow.
2. `git push`: the credential helper needs the keychain or a token; ssh needs keys or `SSH_AUTH_SOCK`. Passing the agent socket through hands the agent every key. Default: push blocked. Correct design: a small host-side broker that holds credentials and forwards only `git push` for this repo. Note: the existing `internal/agent/ssh_bridge.go` has the same hole today.
3. Worktrees and submodules: `gitdir:` and `commondir` pointers escape the cwd. Resolve and bind the referenced paths.
4. Symlinks: Seatbelt matches real paths. Canonicalize every policy path before generating the profile.
5. Environment: use an allowlist, never a denylist. Keep `PATH`, `HOME`, `LANG`, `TERM`, and nothing else ambient. This single scrub removes most ambient credential exposure.
6. `.env` in the project: the agent may read the cwd by definition, so `.env` files must be explicitly denied or they are exposed.
7. The agent's own API key: strongest form is a local proxy that injects the key upstream, so the agent never holds it. Related prior art in this repo: `docs/CREDS-PROXY.md`.

## 7. Redaction minimal list

Files, both OSes: `~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.config/gh`, `~/.netrc`, `~/.docker/config.json`, `~/.kube`, `~/.npmrc`, `~/.pypirc`, `~/.config/gcloud`, `~/.azure`.

Linux only: `/run/user/$UID/` sockets (keyring, gnupg, bus), `/var/run/docker.sock`; a new pid namespace hides other processes' `/proc/*/environ`.

macOS only: `~/Library/Keychains`, browser profile trees, Mach lookups to `securityd` and `com.apple.SecurityServer`.

## 8. Relationship to MXC

Do not embed MXC: it pulls a Rust/cgo toolchain into a Go project that already carries a hard cgo release constraint, and imports a policy surface far larger than the primitive needs. Do not shell out to it yet either.

Take instead:

1. The policy shape: per-path `rw`/`ro`/`deny` plus network egress/ingress/proxy. Keep the thin CLI's policy JSON close to MXC's schema so a later backend swap stays cheap.
2. MXC's per-backend characterization tests as the executable spec for "what breaks".
3. The audit/learning UX: first run records accesses, then the tool proposes the tight policy. Same spirit as `roots_store` learning, better artifacts.
4. MXC processcontainer as a candidate swap-in substrate once it proves stable. bwrap today, MXC tomorrow, no product change.

## 9. Target shape

1. Thin default: `construct <agent>` in any folder starts the host-installed agent under Bubblewrap (Linux) or Seatbelt (macOS). ~10 ms start, no image, no daemon, no provisioning.
2. Heavy tier frozen: the docker and msb backends stay for hostile-repo containment (kernel boundary) and reproducible environments. Opt-in via `[runtime] backend`. Maintenance only; no new features.
3. Windows: deferred. When targeted, MXC's process container is the only sane substrate.
4. Thin path drops `network-filter.sh`; egress control belongs at the policy/proxy layer.
5. The one piece of "agent home" that survives: a scrubbed per-agent config copy (for example a sanitized `~/.claude` clone) bound read-write, so tokens live outside the reachable tree. Note this trades some isolation back; treat as an explicit, documented knob.

## 10. Open questions and next steps

1. Spike (est. 2 days): `construct run` under Bubblewrap on Fedora, host agent, cwd bind, env allowlist, redaction deny list. Exit criterion: claude-code-class agent edits files in cwd and cannot read `~/.ssh` or reach denied hosts.
2. Design the `git push` credential broker (extends `docs/CREDS-PROXY.md`).
3. Adversarial review of this pivot before any code moves.
4. Decide the fate of `ssh_bridge.go` under the primitive (same socket-passthrough hole).
5. Decide whether `roots_store` learning folds into the audit-first policy flow or dies with the heavy tier's mount model.
