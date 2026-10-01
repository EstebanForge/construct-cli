# Embedded msb runtime pair

Status: design, awaiting review.
Owner: Esteban.
Context: 1.17.19 shipped the launch compatibility gate (`ClassifyMsbLaunch`) as a bridge; this doc is the permanent fix.

## Problem

The microsandbox SDK's bundled FFI enforces launch lockstep: a sandbox create succeeds only when the host msb runtime version exactly equals the SDK's own crate version (the legacy `0.6.x <= 18` window excepted). The gate lives inside a compiled dylib we do not own (`superradcompany/microsandbox` is read-only for us), so every msb release breaks microVM launches for every construct user until we bump the SDK and ship. The 1.17.17 dependabot bump to SDK 0.7.3 broke every host that was not msb 0.7.3 — a version that never shipped.

The hand-maintained `MsbSdkPin` constant had already drifted (0.7.2 in constants while go.mod said 0.7.3). Doctor checks cannot save a launch path that refuses at create time.

## Goal

Construct never depends on the host msb. Every microVM launch uses an msb + libkrunfw pair that construct controls, version-matched to the embedded SDK by construction. The user's globally installed msb can be any version — older, newer, or absent — without affecting construct.

## Mechanism

### 1. Build-time embed (release.yml)

Each platform job downloads the msb release bundle pinned to the SDK version and embeds it into the construct binary:

- Assets per platform from `superradcompany/microsandbox` releases: `msb-<os>-<arch>` (47 MB on linux-x86_64) and `libkrunfw-<os>-<arch>.so` (21 MB).
- Version source of truth: `msb.SDKVersion()` of the go.mod SDK — the release workflow resolves it from `go list -m`, never a second pin.
- Verify the upstream `checksums.sha256` before embedding.
- `go:embed` per platform via build-tagged files (`internal/msbembed/darwin_arm64.go`, `.../linux_amd64.go`, ...), each carrying only its own pair, so a construct binary embeds exactly one platform (~68 MB raw, less compressed in release tarballs).

Trade-off accepted: larger release artifacts in exchange for offline safety and deterministic launches. The alternative (first-run download via the SDK's `EnsureRuntime`) stays possible later without changing the call sites, because both paths produce the same extracted pair.

### 2. Extraction

On first microVM use (and after each construct upgrade):

- Extract to `<config>/msb-runtime/<sdk-version>/{msb,libkrunfw}` with mode 0755; verify sha256 against a manifest compiled in at build time.
- Idempotent: matching files short-circuit; a checksum mismatch deletes and re-extracts.
- After a successful extraction of a newer version, delete older version directories (the pair is ~70 MB per version).

### 3. Resolution

All msb usage in construct switches to the extracted pair:

- FFI (in-process): set `MSB_PATH` + `MSB_LIBKRUNFW_PATH` before SDK calls. The FFI reads these from the process environment and resolves the pair without touching the user's installation.
- CLI execs (`msbRun` for image ls/pull/rm/load, list probes): exec the extracted `msb` by absolute path, not `exec.LookPath("msb")`.
- The SDK's exact-version launch contract then passes by construction.

### 4. Home isolation

Schema ping-pong is the remaining hazard when two msb versions share `~/.microsandbox` (the user's newer CLI one-way-migrates the catalog DB that construct's embedded, older CLI must still read). Construct therefore points its msb operations at its own home:

- Target: `<config>/msb-home` via the `MSB_HOME` environment override (existence verified during implementation; the doctor code already documents `MSB_HOME/lib` semantics — if the override turns out not to cover the catalog DB, fall back to a per-invocation config file, and only if that fails too, file the upstream gap).
- Consequence: the image store (~2 GiB after first pull) lives in the construct home. Existing machines re-pull once on upgrade; doctor offers to clean the old store afterwards.

### 5. Fallback policy

No silent fallback to the host msb — that would reintroduce the failure this design removes. Extraction or checksum failure is a clear error suggesting `construct sys doctor --fix` (which re-extracts).

## Doctor changes

- `Host CLI/SDK Skew`: replaced by `Embedded Runtime` — extracted pair present, checksums valid, version equals `msb.SDKVersion()`. The host msb version stays in telemetry only.
- `msb libkrunfw Resolution`: retired (the pair ships together).
- `VM Backend` probe and all suggestion strings exec the embedded msb; "Run `msb list`" style advice disappears from user-facing strings.
- The 1.17.19 `ClassifyMsbLaunch` gate narrows to the extraction check once the embedded pair is the only launch path.

## Test plan

- Unit: extraction idempotence; checksum mismatch fails closed and re-extracts; env wiring reaches the FFI; per-platform embed files carry the right pairs; old-version GC.
- E2E: a host with no msb on PATH launches a sandbox end to end; a host with msb 1.x on PATH is unaffected by it; upgrade flow extracts the new pair and GCs the old.
- Release: artifact size delta reported in the release run summary.

## Rollout

One feature release. The compat gate from 1.17.19 stays active until this ships, then narrows. No data migration beyond the one-time image re-pull; no config format changes.
