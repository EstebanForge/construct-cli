# Brew Exit: Package-by-Package Replacement Analysis

Status: EXECUTED 2026-09-22 (same-day). The Dockerfile, packages.toml ([mise]
replaces [brew]), update-all.sh, entrypoint.sh, topgrade config, agent-patch,
compose PATH, and env.go PathComponents are all migrated; the [mise] user tier
installs via `mise use -g`. Owner calls resolved: Node via NodeSource 24, Go
via go.dev tarball 1.27.x, rust/kotlin/scala/groovy/gradle on demand via mise,
niceties kept where apt provides them, fastmod dropped (no release assets).
Restore-table recipes in PACKAGES.md now use apt/mise. This document is kept
as the analysis record; the execution deltas live in the git history.

Owner decision 2026-09-22: Homebrew leaves the baked image. Every declared brew formula migrates to Debian official packaging (trixie) where adequate, or to that package's own official install channel where not. This document preserves the full analysis so the migration loses nothing.

## Why

- Post-trim image is 15.6 GB; the brew Cellar alone is 11 GB (296 kegs, top weights: llvm@22 2.47 GB, dart-sdk 0.63 GB, brew gcc 0.45 GB). The apt layer serves 603 packages in 1.72 GiB because Debian shares libraries; brew vendors per bottle and stacked a second userspace on top of the first.
- brew on Linux also duplicated things apt already provides (openssl, python, curl, make, imagemagick, ffmpeg) and pulled private copies of shared infra (three llvm majors at peak, mesa/gtk chain via erlang, removed 2026-09-22).
- Debian base is already `debian:trixie-slim` (13, current stable), so trixie packaging is materially fresher than the "Debian is outdated" stereotype: awscli 2.23, podman 5.4, redis 8.0, r-base 4.5, ocaml 5.3, micropython 1.25, openjdk 21 AND 25, gum, procs, eza, git-delta all present and current.

## Method

1. Declared formulae extracted from `internal/templates/Dockerfile` (101 across 8 install chunks plus the shivammathur php loop). Transitive deps are not listed; they vanish with the declared set.
2. Live probe: `apt-get update` + `apt-cache policy` inside a container of the current image (trixie) for every mapped candidate. Versions quoted below come from that probe.
3. Official channels verified against primary sources (dart.dev, swift.org, cli.github.com, golangci-lint.run, wp-cli.org, ziglang.org, topgrade-rs, nodesource/distributions, mikefarah/yq, rtk-ai/rtk).
4. Non-brew extras (agents, bun/qmd, litellm, mise, asdf) audited: they already install via official channels and are unchanged by this migration.

## Verdict legend

- `APT` — trixie package, current enough, drop-in.
- `APT+REPO` — official vendor apt repository (signed, documented by the vendor as the recommended Linux path).
- `BIN` — official prebuilt binary (GitHub release, phar, or piped install script) into /usr/local/bin.
- `NPM` / `GO-INSTALL` / `CARGO` — official language-manager channel.
- `MISE/SDKMAN` — on-demand runtime managers; bake or defer to first use (owner call).
- `DROP` — no longer earns its weight; removal documented in PACKAGES.md.

## R1 core-utils chunk (39 formulae)

| brew | trixie candidate | verdict | replacement |
|---|---|---|---|
| ast-grep | absent | BIN/NPM | `npm -g @ast-grep/cli` (official) or GitHub release binary |
| yq | yq 3.4.3 (python kislyuk wrapper) | BIN | NOT the same tool; mikefarah yq v4 via GitHub release binary (official) |
| sd | sd 1.0.0 | APT | |
| fzf | fzf 0.60.3 | APT | |
| eza | eza 0.21.0 | APT | |
| fd | fd-find 10.2.0 | APT | binary is `fdfind`; add `fd` symlink |
| bat | bat 0.25.0 | APT | binary is `batcat`; add `bat` symlink |
| ripgrep | ripgrep 14.1.1 | APT | |
| jq | jq 1.7.1 | APT | |
| curl, wget, tree, unzip, socat, make, nano, tmux, rsync | already in apt tier | APT | delete from brew (dupes) |
| htop | htop 3.4.1 | APT | |
| netcat | netcat-openbsd 1.229 | APT | |
| bind | bind9-dnsutils 9.20 | APT | already in apt tier |
| sshpass | sshpass 1.10 | APT | |
| lftp | lftp 4.9.2 | APT | |
| btop | btop 1.3.2 | APT | |
| imagemagick | imagemagick 7.1.1 | APT | same 7.x major as brew |
| topgrade | absent | BIN | GitHub release binary (official per topgrade-rs docs; cargo build too slow) |
| libgit2 | libgit2-dev 1.9.0 | APT | only needed if something links it; verify consumer, else DROP |
| gh | gh 2.46.0 | APT+REPO | works; GitHub's official apt repo (cli.github.com) for current releases |
| git-lfs | git-lfs 3.6.1 | APT | |
| git-delta | git-delta 0.18.2 | APT | |
| git-cliff | absent | BIN | GitHub release binary (official) or cargo |
| procs | procs 0.14.10 | APT | |
| python-setuptools | python3-setuptools 78.1.1 | APT | |
| awscli | awscli 2.23.6 (v2!) | APT | trixie ships v2, current |
| cmake | cmake 3.31.6 | APT | |
| pkg-config | pkgconf 1.8.1 | APT | |
| direnv | direnv 2.32.1 | APT | |
| gum | gum 0.14.4 | APT | charm tool, packaged in trixie |
| rtk | absent | BIN | github.com/rtk-ai/rtk, single Rust binary, GitHub release (official) |

## R2 languages chunk (7)

| brew | trixie candidate | verdict | replacement |
|---|---|---|---|
| go | golang-go 1.24 | BIN | go.dev/dl official tarball for current (brew had 1.27); apt 1.24 is the fallback. Owner call. |
| openjdk | openjdk-21-jdk AND openjdk-25-jdk | APT | openjdk-25-jdk is current LTS |
| typescript | (npm) | NPM | `npm -g typescript` |
| rust | rustc (old) | BIN | rustup official (minimal profile); see owner calls |
| kotlin | kotlin 1.3.31 (2019) | MISE/SDKMAN | apt version useless |
| lua | lua5.4 5.4.7 | APT | |
| ruby | ruby-full 3.3 | APT | brew had 3.4; jekyll installs fine on 3.3 |

## R3 dart/perl/gnucobol chunk (3)

| brew | trixie candidate | verdict | replacement |
|---|---|---|---|
| dart-sdk | absent | APT+REPO | Google's official signed dart apt repo per dart.dev/get-dart (Debian 12/13 documented) |
| perl | perl (base) | APT | dupe, drop from brew |
| gnucobol | gnucobol3 3.2 | APT | same 3.2 as brew (gnucobol4 is an early snapshot, avoid) |

## R4 node/python chunk (7)

| brew | trixie candidate | verdict | replacement |
|---|---|---|---|
| node | nodejs 20.19 | APT+REPO | trixie 20 is PAST upstream EOL (Apr 2026); NodeSource official setup_24.x for current LTS. Owner call but 24 is the honest choice. |
| python@3.12, python@3 | python3 3.13.5 | APT | 3.13 covers both; version pinning is uv/mise's job |
| openssl@3 | libssl3/openssl (system) | APT | dupe |
| gcc | build-essential (gcc 14/15) | APT | already in apt tier; brew gcc 16 vanishes |
| nasm, yasm | nasm 2.16, yasm 1.3 | APT | |

## R5 db/misc chunk (12)

| brew | trixie candidate | verdict | replacement |
|---|---|---|---|
| postgresql@16 | postgresql-client-17 17.11 | APT | newer client |
| mysql-client | default-mysql-client (MariaDB) | APT | protocol-compatible; note in docs |
| redis | redis-server 8.0.2 (+tools) | APT | current |
| sqlite | sqlite3 3.46.1 | APT | |
| podman | podman 5.4.2 | APT | current |
| podman-compose | podman-compose 1.3.0 | APT | |
| groovy | groovy 2.4.21 (ancient) | MISE/SDKMAN | |
| scala | scala 2.11 (ancient) | MISE/SDKMAN | coursier is the official launcher |
| r | r-base 4.5.0 | APT | current |
| micropython | micropython 1.25.0 | APT | current |
| ocaml | ocaml 5.3.0 | APT | |
| swi-prolog | swi-prolog 9.2.9 | APT | |

## R7 tooling chunk (26)

| brew | trixie candidate | verdict | replacement |
|---|---|---|---|
| golangci-lint | absent | BIN | official `golangci-lint.run/install.sh` piped script |
| golangci-lint-langserver | absent | GO-INSTALL | `go install github.com/nametake/golangci-lint-langserver@latest` |
| php-cs-fixer | absent | BIN | official phar from cs.symfony.com |
| prettier | (npm) | NPM | `npm -g prettier` |
| shellcheck | shellcheck 0.10.0 | APT | |
| yamllint | yamllint 1.37.1 | APT | |
| yarn | yarnpkg 4.1 (or corepack) | APT | corepack ships with NodeSource node; either fine |
| pnpm | (corepack) | APT | corepack enable |
| composer | composer 2.8.8 | APT | current |
| neovim | neovim 0.10.4 | APT | brew 0.11 delta acceptable |
| gulp-cli | (npm) | NPM | |
| ffmpeg | ffmpeg 7.1 | APT | current major |
| wp-cli | absent | BIN | official phar per wp-cli.org / make.wordpress.org |
| tailwindcss | (npm) | NPM | `npm -g tailwindcss` (standalone binary also official) |
| uv | absent | BIN | official astral.sh piped script or pipx |
| pipx | pipx 1.7.1 | APT | |
| vite, webpack | (npm) | NPM | |
| tlrc | absent | BIN/CARGO | GitHub release binary; trixie has no tldr client |
| ninja | ninja-build 1.12.1 | APT | |
| gradle | gradle (ancient) | MISE/SDKMAN | official zip or sdkman |
| fastmod | absent | CARGO | cargo install fastmod; niche, candidate DROP |
| hugo | hugo 0.131 | APT | slightly old; BIN upgrade path exists |
| zola | absent | BIN | GitHub release binary (official) |
| EstebanForge/tap/mcp-cli-ent | absent | BIN | own tool; install from EstebanForge GitHub release |
| EstebanForge/tap/md-over-here | absent | BIN | same |

## R8 php loop (shivammathur tap)

| brew | trixie candidate | verdict | replacement |
|---|---|---|---|
| php@8.2/8.3/8.4/8.5/8.6 (five side-by-side) | php8.4-cli (+ext) | APT | ONE current version from trixie; multi-major users go mise or sury (documented, not baked). Verify `php8.4-pcov` extension package name at execution. |
| pcov@8.2/8.3/8.4 | php8.4-pcov | APT | single version |
| bare php 8.5 (via php-cs-fixer dep) | php8.4 | APT | `php` on PATH comes from php8.4-cli |

## Restore paths for previously removed tools (post-brew-exit)

| Tool | old restore | new restore |
|---|---|---|
| llvm/clang | `[brew] packages = ["llvm"]` | `apt install clang` (shared libLLVM, ~1.5 GB total, not 7.34 GiB) |
| swift | `[brew] packages = ["swift"]` | swift.org officially supports Debian; swiftly installer or tarball |
| zig | `[brew] packages = ["zig"]` | ziglang.org signed tarball or mise |
| erlang/elixir/gleam | `[brew] packages = [...]` | `apt install erlang elixir` (27.3 / 1.18.3 confirmed live); gleam via mise |

## Already-official, unchanged

Five canonical agents (claude curl installer, codex npm, agy curl installer, pi npm, opencode curl installer), optional npm agents (qwen, copilot, crush, cline, kilocode, agent-browser, url-to-markdown, sass, hunkdiff), bun + qmd (bun.sh piped script), litellm (pipx), mise (mise.run script), asdf (git clone). All keep their official channels; the migration touches none of them.

## Projected size

Cellar -11 GB, brew prefix/tooling -0.5 GB. Apt additions land mostly in the existing 1.72 GiB layer; incremental apt weight is dominated by r-base, dart (~0.7 GB), openjdk-25 (~0.55 GB), ffmpeg, imagemagick, redis, podman, go-or-empty, rust-or-empty, plus ~0.15 GB of standalone binaries. Realistic projection: **~6-8 GB image** depending on the owner calls below and `--no-install-recommends` discipline.

## Owner calls (open)

1. **Node**: trixie 20.19 (EOL) vs NodeSource 24 LTS. Recommend 24.
2. **Go**: trixie 1.24 vs go.dev tarball current. Recommend tarball (pinned, official).
3. **Rust**: rustup baked minimal (~1 GB) vs mise-on-demand (0 GB baked, first-use download). Recommend mise-on-demand for size; revisit if offline-first matters more.
4. **JVM extras** (kotlin, scala, groovy, gradle): bake via mise/sdkman (~1.2 GB) vs mise-on-demand (0 baked). Recommend mise-on-demand.
5. **rtk / fastmod / sd / procs / btop tier of niceties**: keep all, or trim niceties. Keep unless size-critical.

## BIN installer mechanism (GitHub-release tools)

Researched 2026-09-22 (owner question: nanobrew/gpm-style binary managers). Verdicts: nanobrew is a Zig Homebrew/.deb client, not a release installer (7 months old, telemetry on) - rejected. gpm is dead since 2023 and git-LFS based - rejected. eget dormant since 2024 (sha256 yes, no state). ubi active but no checksum verification at all. bin can prompt interactively mid-build and verifies nothing. pkgx serves its own pantry, not upstream releases.

Decision: mise `github:` backend for the ~8 tools with no better channel (yq, topgrade, git-cliff, zola, tlrc, rtk, EstebanForge/tap/mcp-cli-ent, EstebanForge/tap/md-over-here). One baked system-level mise config pins exact versions with checksums; GitHub Artifact Attestations verify by default; `mise install --locked` against a committed mise.lock gives byte-level reproducibility; topgrade already updates mise tools in-guest for the user layer. Vendor official scripts stay first where they exist (golangci-lint install.sh, wp-cli and php-cs-fixer phars, uv astral script). Fallback if mise asset matching fights a release layout: aquaproj/aqua (declarative pins, committed checksums, Renovate, multistage `aqua cp` strips the manager from the final image) - rejected as primary only because it adds a second manager alongside mise.

## Execution plan (when approved)

1. Dockerfile rewrite: expand apt tier (with `--no-install-recommends`), add vendor repos (dart, gh, NodeSource), vendor-script installs (golangci-lint, uv, wp-cli, php-cs-fixer), bake a system-level mise config with `github:`-pinned versions for the 8 release-fetch tools, kill brew env/taps/chunks/php loop, add fdfind/bat symlinks.
2. `GenerateInstallScript` / packages.toml: `[brew]` tier removed; `[apt]` stays; docs point restore recipes at apt/mise. Doctor `StaleBakedCopyPaths` unaffected (agent bind copies).
3. `update-all.sh`: swap brew/topgrade source assumptions for apt + npm + mise paths.
4. AGENTS.md "Installed Tools" wording (currently says "via Homebrew").
5. Docs: PACKAGES.md baseline sections + restore table rewritten to apt recipes; this document updated with execution deltas.
6. Rebuild, docker save, msb load, full lab-matrix gate, VMsv2 section 10 numbers.
7. Agy review round before commit, per protocol.

## Risks

- Guest scripts/agents that assume `brew` on PATH will need apt equivalents; grep templates + docs for `brew` references at execution.
- `bat`/`fd` binary-name deltas (batcat/fdfind) break muscle memory; symlinks close it.
- postgres client 17 vs 16 servers is wire-compatible; MariaDB client vs MySQL servers is compatible for normal use; both noted in docs.
- php single-version: workspaces pinning 8.2/8.3 must move to mise (documented).
- NodeSource/dart/gh vendor repos must be pinned and GPG-verified; they add third-party trust to the build. Vendor repos are signed and are each vendor's documented official path.
