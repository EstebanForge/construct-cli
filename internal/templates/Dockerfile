# syntax=docker/dockerfile:1
ARG BASE_IMAGE=construct-base

# ==============================================================================
# TIER 1: Base image (system packages, dev tools, runtimes, managers)
# Published to ghcr.io/estebanforge/construct-base:latest via base-image.yml
# ==============================================================================
FROM debian:trixie-slim AS construct-base

# Keep downloaded .debs in the apt cache: the guest /var/cache/apt/archives
# is a persistent host bind, so recreated sandboxes re-provision
# packages.toml from local copies instead of re-downloading. debian docker
# base images ship docker-clean, which purges the archive after every apt
# run — remove it or the cache bind stays empty.
RUN rm -f /etc/apt/apt.conf.d/docker-clean && \
    echo 'APT::Keep-Downloaded-Packages "true";' > /etc/apt/apt.conf.d/construct-keep-cache

# 1. Create non-root user and install system packages
RUN useradd -u 1000 -m -s /bin/bash construct && \
    echo "construct:construct" | chpasswd && \
    apt-get update && apt-get install -y \
    build-essential git curl wget sudo gosu socat bubblewrap xvfb unzip \
    chromium chromium-driver chromium-sandbox \
    fonts-liberation libappindicator3-1 libasound2 libatk-bridge2.0-0 \
    libatk1.0-0 libcups2 libdbus-1-3 libdrm2 libgbm1 libgtk-3-0 \
    libnspr4 libnss3 libx11-xcb1 libxcomposite1 libxdamage1 libxrandr2 \
    gnome-keyring libsecret-1-0 dbus-x11 xdg-utils libcap2-bin \
    procps file ufw iptables bind9-dnsutils xclip xsel wl-clipboard \
    python3 python3-pip python3-venv python3-dev \
    gnupg vim tmux nano && \
    rm -rf /var/lib/apt/lists/*
    # [apt] from packages.toml is baked here (vim/tmux/nano; g++ ships in
    # build-essential). The runtime installer no longer manages this tier.
    # NOTE: libcap2-bin provides the `setcap` binary used at runtime by
    # entrypoint.sh to grant socat the privileged-port bind cap. The file cap is
    # applied in the entrypoint (as root), NOT here: BuildKit's default sandbox
    # blocks file-capability writes during `docker build`.

# Allow the construct user full passwordless sudo. The microVM is the
# security boundary: guest root adds no host reach beyond the mounts
# already granted (read-only virtiofs stays read-only even for root),
# and agents should never hit a sudo wall mid-task. entrypoint.sh
# re-applies this file at every boot so persistent guest root disks from
# older images heal in place without a recreate.
RUN echo "construct ALL=(ALL:ALL) NOPASSWD:ALL" > /etc/sudoers.d/construct && \
    chmod 0440 /etc/sudoers.d/construct

# Hash-gate dir (root-fs install gate): pre-created owned by construct so
# non-root runtimes (non_root_strict, rootless podman) can write the gate
# without the entrypoint's root phase.
RUN mkdir -p /var/lib/construct-cli /opt/toolchains && chown construct:construct /var/lib/construct-cli /opt/toolchains

# 2. Base environment (brew exited 2026-09-22; see docs/BREW-EXIT.md).
# Every former brew formula now ships from trixie, a vendor repo, an
# official install script/phar, npm, or the baked mise github: tier.
# NOTE: Keep this PATH list in sync with:
# - internal/env/env.go (BuildConstructPath)
# - internal/templates/entrypoint.sh
# - internal/templates/docker-compose.yml
# User-level dirs lead (user installs shadow baked baseline); system mise
# shims and go follow; distro paths last.
ENV PATH="/home/construct/.local/bin:/home/construct/.npm-global/bin:/home/construct/.cargo/bin:/home/construct/.bun/bin:/home/construct/.opencode/bin:/home/construct/.asdf/bin:/home/construct/.asdf/shims:/home/construct/.local/share/mise/bin:/home/construct/.local/share/mise/shims:/home/construct/.volta/bin:/home/construct/.local/share/pnpm:/home/construct/.yarn/bin:/home/construct/.config/yarn/global/node_modules/.bin:/home/construct/go/bin:/home/construct/.composer/vendor/bin:/home/construct/.config/composer/vendor/bin:/home/construct/.nix-profile/bin:/nix/var/nix/profiles/default/bin:/home/construct/.phpbrew/bin:/usr/local/share/mise/shims:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
# node-gyp python hook (Debian python3; the old brew python3.12 pin is gone)
ENV PYTHON=/usr/bin/python3
# Configure Puppeteer to use system Chromium (prevents arch mismatch on arm64)
ENV PUPPETEER_SKIP_CHROMIUM_DOWNLOAD=true
ENV PUPPETEER_EXECUTABLE_PATH=/usr/bin/chromium

# 3. Bake the dev baseline - apt tier (replaces the brew chunks).
# Language toolchains (Go, Rust, Java, PHP, etc.) are modular OCI artifacts
# mounted on-demand via internal/toolchains.
RUN apt-get update && apt-get install -y --no-install-recommends \
    sd fzf eza fd-find bat ripgrep jq htop netcat-openbsd sshpass lftp \
    btop imagemagick git-lfs git-delta procs python3-setuptools awscli \
    cmake pkgconf direnv gum \
    lua5.4 ruby-full ruby-dev nasm yasm \
    sqlite3 podman podman-compose \
    shellcheck yamllint neovim ffmpeg ninja-build hugo pipx && \
    ln -sf /usr/bin/fdfind /usr/local/bin/fd && \
    ln -sf /usr/bin/batcat /usr/local/bin/bat && \
    rm -rf /var/lib/apt/lists/*
# fd/bat: Debian ships fd-find/bat as fdfind/batcat; the symlinks restore
# the upstream names agents and humans expect.

# 4. Vendor repositories: gh (GitHub), nodejs 24 (NodeSource).
# Each is the vendor's own documented, signed apt path for current releases.
# (dart was removed 2026-09-22 for image weight, ~640 MB — restore via the
# Google apt repo per dart.dev/get-dart; see docs/PACKAGES.md restore table.)
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg -o /usr/share/keyrings/githubcli-archive-keyring.gpg && \
    chmod go+r /usr/share/keyrings/githubcli-archive-keyring.gpg && \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" > /etc/apt/sources.list.d/github-cli.list && \
    curl -fsSL https://deb.nodesource.com/setup_24.x -o /tmp/nodesource_setup.sh && \
    bash /tmp/nodesource_setup.sh && \
    apt-get install -y --no-install-recommends gh nodejs && \
    rm -f /tmp/nodesource_setup.sh && rm -rf /var/lib/apt/lists/*

# 5. mise: the manager binary, then the baked GitHub-release tool tier.
# The system config pins exact versions; mise verifies GitHub Artifact
# Attestations by default where a release publishes them. Installs land in
# /usr/local/share/mise (root fs, recreated per sandbox; root-owned is
# fine — runtime users only exec the symlinked binaries).
COPY mise-tools.toml /etc/mise/config.toml
# GITHUB_TOKEN comes in as a buildkit secret (never baked into the image);
# authenticated API requests avoid the 60/hr anonymous rate limit. Without
# the secret the tier still installs, just unauthenticated.
# Tools install into /usr/local/share/mise then their binaries are
# symlinked into /usr/local/bin — no shims, so runtime mise (user data dir)
# never needs to resolve the baked tier, and user-layer mise tools still
# shadow these via the earlier ~/.local/share/mise/shims PATH entry.
RUN --mount=type=secret,id=github_token \
    if [ -f /run/secrets/github_token ]; then export GITHUB_TOKEN="$(cat /run/secrets/github_token)"; fi && \
    curl -fsSL https://mise.run | MISE_INSTALL_PATH=/usr/local/bin/mise sh && \
    MISE_CONFIG_DIR=/etc/mise MISE_DATA_DIR=/usr/local/share/mise mise install && \
    for spec in github:mikefarah/yq@4.53.6 github:topgrade-rs/topgrade@17.7.0 github:orhun/git-cliff@2.14.2 github:getzola/zola@0.23.6 github:tldr-pages/tlrc@1.13.1 github:rtk-ai/rtk@0.49.0 github:EstebanForge/mcp-cli-ent@1.4.0 github:EstebanForge/md-over-here@1.0.0; do \
      bindir=$(MISE_CONFIG_DIR=/etc/mise MISE_DATA_DIR=/usr/local/share/mise mise bin-paths "$spec") && \
      find "$bindir" -maxdepth 1 -executable ! -type d ! -name "*man-page*" -exec ln -sf {} /usr/local/bin/ \; || exit 1; \
    done && \
    rm -f /usr/local/bin/yq_linux_* && \
    yq_dir="$(MISE_CONFIG_DIR=/etc/mise MISE_DATA_DIR=/usr/local/share/mise mise bin-paths github:mikefarah/yq@4.53.6)" && \
    yq_bin="$(find "$yq_dir" -maxdepth 1 -type f -name 'yq_linux_*' -executable | head -1)" && \
    ln -sf "$yq_bin" /usr/local/bin/yq && \
    for t in yq topgrade git-cliff zola tldr rtk mcp-cli-ent md-over-here; do \
      "$t" --version >/dev/null 2>&1 || { echo "FATAL: baked tool not executable: $t" >&2; exit 1; }; \
    done

# 6. npm tier (NodeSource node; upstream prefix /usr/local)
RUN npm install -g --prefix /usr/local \
    typescript prettier @ast-grep/cli yarn pnpm gulp-cli tailwindcss \
    vite webpack acpx @colbymchenry/codegraph

# 7. Vendor-script installs: uv (astral), golangci-lint (official script),
# and golangci-lint-langserver (official release binary).
RUN curl -LsSf https://astral.sh/uv/install.sh | UV_INSTALL_DIR=/usr/local/bin INSTALLER_NO_MODIFY_PATH=1 sh && \
    curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b /usr/local/bin v2.13.2 && \
    case "$(dpkg --print-architecture)" in \
      amd64) GLL_ARCH="Linux_x86_64" ;; \
      arm64) GLL_ARCH="Linux_arm64" ;; \
    esac && \
    curl -fsSL "https://github.com/nametake/golangci-lint-langserver/releases/download/v0.0.12/golangci-lint-langserver_${GLL_ARCH}.tar.gz" -o /tmp/gll.tgz && \
    tar -C /usr/local/bin -xzf /tmp/gll.tgz golangci-lint-langserver && rm -f /tmp/gll.tgz && \
    uv --version && golangci-lint --version | head -1

# 8. Bake remaining dev tooling to SYSTEM paths (root-owned).
# Ruby gems -> apt ruby's site dir (jekyll version-pinned; users shadow
# via gem install into the home bind); Python tools via pipx -> /usr/local/bin
# (litellm version-pinned with the proxy extra — the CLI needs its deps;
# keeps user pipx overrides in $HOME/.local/bin ahead of it); bun binary ->
# /usr/local/bun with runtime globals staying in ~/.bun (user tier); asdf
# baked from the tagged release tarball (asdf's Go rewrite ships a compiled
# binary — cloning the repo gives source, not a tool). Its data dir
# initializes in-home at runtime (cheap, no network). mise itself is
# installed in step 6.
ARG ASDF_VERSION=0.20.2
USER root
RUN gem install jekyll -v 4.4.1 && \
    PIPX_HOME=/usr/local/pipx PIPX_BIN_DIR=/usr/local/bin pipx install "litellm[proxy]==1.102.0" && \
    curl -fsSL https://bun.sh/install | BUN_INSTALL=/usr/local/bun bash && \
    ln -sf /usr/local/bun/bin/bun /usr/local/bin/bun && \
    ln -sf /usr/local/bun/bin/bunx /usr/local/bin/bunx && \
    bun --version && \
    curl -fsSL "https://github.com/asdf-vm/asdf/releases/download/v${ASDF_VERSION}/asdf-v${ASDF_VERSION}-linux-$(dpkg --print-architecture).tar.gz" -o /tmp/asdf.tgz && \
    tar -C /usr/local/bin -xzf /tmp/asdf.tgz && rm -f /tmp/asdf.tgz && \
    asdf --version && litellm --version | head -1 && jekyll --version

# ==============================================================================
# TIER 2: Construct Box (agents, tools, wrappers, permissions)
# Published to ghcr.io/estebanforge/construct-box:latest via image.yml
# ==============================================================================
FROM ${BASE_IMAGE} AS construct-box

# Copy entrypoint scripts (still USER root from step 3b)
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
COPY update-all.sh /usr/local/bin/update-all.sh
COPY clipper /usr/local/bin/clipper
COPY clipboard-x11-sync.sh /usr/local/bin/clipboard-x11-sync.sh
COPY osascript /usr/local/bin/osascript
COPY construct-host-exec /usr/local/bin/construct-host-exec
# We don't COPY install_user_packages.sh here because it is generated at runtime
# and mounted via docker-compose.override.yml.
RUN chmod +x /usr/local/bin/entrypoint.sh /usr/local/bin/update-all.sh /usr/local/bin/clipper /usr/local/bin/clipboard-x11-sync.sh /usr/local/bin/osascript /usr/local/bin/construct-host-exec

# Create symlink for g++-11 to point to g++ (for node-gyp compatibility)
# Some native Node.js modules are hardcoded to use g++-11, but Debian Trixie has g++-14
RUN ln -sf /usr/bin/g++ /usr/bin/g++-11 && \
    ln -sf /usr/bin/gcc /usr/bin/gcc-11

# 5. Setup Clipboard Shim
# Rename original tools and link them to clipper
RUN if [ -f /usr/bin/xclip ]; then mv /usr/bin/xclip /usr/bin/xclip-real; fi && \
    ln -sf /usr/local/bin/clipper /usr/bin/xclip && \
    if [ -f /usr/bin/xsel ]; then mv /usr/bin/xsel /usr/bin/xsel-real; fi && \
    ln -sf /usr/local/bin/clipper /usr/bin/xsel && \
    if [ -f /usr/bin/wl-paste ]; then mv /usr/bin/wl-paste /usr/bin/wl-paste-real && ln -sf /usr/local/bin/clipper /usr/bin/wl-paste; fi

# Bake default npm tools: copilot, agent-browser, url-to-markdown, sass, hunkdiff
RUN rm -f /usr/local/bin/sass && \
    npm install -g --force --prefix /usr/local \
        @github/copilot agent-browser url-to-markdown-cli-tool sass hunkdiff

# Wrap agent-browser to use Debian's system Chromium (no Playwright download needed).
# Remove npm's symlink first so printf does not follow it and overwrite agent-browser.js.
RUN rm -f /usr/local/bin/agent-browser && \
    printf '#!/bin/bash\nexec /usr/bin/node /usr/local/lib/node_modules/agent-browser/bin/agent-browser.js --executable-path /usr/bin/chromium "$@"\n' > /usr/local/bin/agent-browser && \
    chmod +x /usr/local/bin/agent-browser

# 6. Bake core agents and secondary tools (amp, goose, droid, obscura, claude, agy, opencode, codex, pi, pig)
# - pig: pi-in-go.dev 403s GHA egress; fetched from MichaelKinsy/PiG raw repo with PIG_VERSION pinned
# - agy: antigravity.google sometimes returns gzip without Content-Encoding header; detect magic bytes and gunzip
# - pi: managed installer needs PI_CODING_AGENT_DIR so launcher stays inside /usr/local/lib/pi-agent
RUN HOME=/tmp/agent-install && mkdir -p "$HOME" && \
    curl -fsSL https://claude.ai/install.sh | bash && \
    install -m0755 "$(find "$HOME" \( -type f -o -type l \) -name claude | sort | head -1)" /usr/local/bin/claude && \
    curl -fsSL https://antigravity.google/cli/install.sh -o "$HOME/agy-install.sh" && \
    if [ "$(head -c 2 "$HOME/agy-install.sh" | od -An -tx1 | tr -d ' \n')" = "1f8b" ]; then \
      mv "$HOME/agy-install.sh" "$HOME/agy-install.sh.gz" && gunzip -f "$HOME/agy-install.sh.gz"; \
    fi && \
    bash "$HOME/agy-install.sh" && \
    install -m0755 "$(find "$HOME" \( -type f -o -type l \) -name agy | sort | head -1)" /usr/local/bin/agy && \
    curl -fsSL https://opencode.ai/install | bash && \
    install -m0755 "$(find "$HOME" \( -type f -o -type l \) -name opencode | sort | head -1)" /usr/local/bin/opencode && \
    npm install -g --prefix /usr/local @openai/codex && \
    curl -fsSL https://pi.dev/install.sh | PI_CODING_AGENT_DIR=/usr/local/lib/pi-agent sh && \
    ln -sf /usr/local/lib/pi-agent/bin/pi /usr/local/bin/pi && \
    curl -fsSL https://raw.githubusercontent.com/MichaelKinsy/PiG/main/docs/site/public/install.sh | PIG_VERSION=0.2.0 PIG_INSTALL_DIR=/usr/local/bin sh && \
    curl -fsSL https://ampcode.com/install.sh | bash && \
    amp_bin="$(find "$HOME" -name amp -type f -perm -0100 2>/dev/null | head -1)" && \
    [ -n "$amp_bin" ] && install -m0755 "$amp_bin" /usr/local/bin/amp && \
    curl -fsSL https://github.com/aaif-goose/goose/releases/download/stable/download_cli.sh | CONFIGURE=false bash && \
    goose_bin="$(find "$HOME" -name goose -type f -perm -0100 2>/dev/null | head -1)" && \
    [ -n "$goose_bin" ] && install -m0755 "$goose_bin" /usr/local/bin/goose && \
    curl -fsSL https://app.factory.ai/cli | sh && \
    droid_bin="$(find "$HOME" -name droid -type f -perm -0100 2>/dev/null | head -1)" && \
    [ -n "$droid_bin" ] && install -m0755 "$droid_bin" /usr/local/bin/droid && \
    case "$(dpkg --print-architecture)" in amd64) OBS_ARCH=x86_64;; arm64) OBS_ARCH=aarch64;; esac && \
    curl -fsSL -o /tmp/obscura.tar.gz "https://github.com/h4ckf0r0day/obscura/releases/download/v0.1.9/obscura-${OBS_ARCH}-linux.tar.gz" && \
    tar -xzf /tmp/obscura.tar.gz -C /usr/local/bin obscura obscura-worker && \
    chmod +x /usr/local/bin/obscura /usr/local/bin/obscura-worker && \
    rm -rf /tmp/obscura.tar.gz "$HOME" && \
    for c in claude agy opencode codex pi pig amp goose droid obscura copilot agent-browser; do \
      "$c" --version >/dev/null 2>&1 || { echo "FATAL: baked tool failed execution: $c" >&2; exit 1; }; \
    done

# Agent tier is user-writable
RUN chown -R construct:construct /usr/local/lib/node_modules /usr/local/lib/pi-agent && \
    chown construct:construct /usr/local/bin && \
    chown -h construct:construct /usr/local/bin/claude /usr/local/bin/agy \
        /usr/local/bin/opencode /usr/local/bin/codex /usr/local/bin/pi \
        /usr/local/bin/pig /usr/local/bin/amp /usr/local/bin/goose \
        /usr/local/bin/droid /usr/local/bin/obscura /usr/local/bin/copilot \
        /usr/local/bin/agent-browser && \
    chmod +t /usr/local/bin

WORKDIR /projects
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["/bin/bash"]
