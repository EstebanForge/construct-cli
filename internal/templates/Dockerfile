FROM debian:trixie-slim

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

# Allow construct user to run privileged commands without password
# Include chown for Podman rootless permission fixes
RUN echo "construct ALL=(ALL) NOPASSWD: /usr/bin/apt*, /usr/sbin/apt*, /usr/bin/apt-get, /usr/sbin/apt-get, /usr/sbin/ufw, /bin/chown, /usr/bin/chown" >> /etc/sudoers.d/construct && \
    chmod 0440 /etc/sudoers.d/construct

# Hash-gate dir (root-fs install gate): pre-created owned by construct so
# non-root runtimes (non_root_strict, rootless podman) can write the gate
# without the entrypoint's root phase.
RUN mkdir -p /var/lib/construct-cli && chown construct:construct /var/lib/construct-cli

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
# All versions verified live in trixie on 2026-09-22 (docs/BREW-EXIT.md has
# the full per-formula mapping). Single layer: apt layers are small and the
# msb 10 GiB per-layer limit is nowhere near.
RUN apt-get update && apt-get install -y --no-install-recommends \
    sd fzf eza fd-find bat ripgrep jq htop netcat-openbsd sshpass lftp \
    btop imagemagick git-lfs git-delta procs python3-setuptools awscli \
    cmake pkgconf direnv gum \
    lua5.4 ruby-full ruby-dev gnucobol3 nasm yasm \
    postgresql-client-17 default-mysql-client redis-server redis-tools \
    sqlite3 podman podman-compose micropython ocaml swi-prolog r-base \
    shellcheck yamllint composer neovim ffmpeg ninja-build hugo pipx \
    php8.4-cli php8.4-mbstring php8.4-xml php8.4-curl php8.4-intl \
    php8.4-pcov openjdk-25-jdk && \
    ln -sf /usr/bin/fdfind /usr/local/bin/fd && \
    ln -sf /usr/bin/batcat /usr/local/bin/bat && \
    rm -rf /var/lib/apt/lists/*
# fd/bat: Debian ships fd-find/bat as fdfind/batcat; the symlinks restore
# the upstream names agents and humans expect.

# 4. Vendor repositories: gh (GitHub), dart (Google), nodejs 24 (NodeSource).
# Each is the vendor's own documented, signed apt path for current releases.
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg -o /usr/share/keyrings/githubcli-archive-keyring.gpg && \
    chmod go+r /usr/share/keyrings/githubcli-archive-keyring.gpg && \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" > /etc/apt/sources.list.d/github-cli.list && \
    curl -fsSL https://dl-ssl.google.com/linux/linux_signing_key.pub | gpg --dearmor -o /usr/share/keyrings/dart.gpg && \
    echo "deb [signed-by=/usr/share/keyrings/dart.gpg] https://storage.googleapis.com/download.dartlang.org/linux/debian stable main" > /etc/apt/sources.list.d/dart.list && \
    curl -fsSL https://deb.nodesource.com/setup_24.x -o /tmp/nodesource_setup.sh && \
    bash /tmp/nodesource_setup.sh && \
    apt-get install -y --no-install-recommends gh dart nodejs && \
    rm -f /tmp/nodesource_setup.sh && rm -rf /var/lib/apt/lists/*

# 5. Go from the official go.dev tarball (current; trixie's golang is 1.24).
ARG GO_VERSION=1.27.1
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-$(dpkg --print-architecture).tar.gz" -o /tmp/go.tgz && \
    tar -C /usr/local -xzf /tmp/go.tgz && rm /tmp/go.tgz && \
    /usr/local/go/bin/go version

# 6. mise: the manager binary, then the baked GitHub-release tool tier.
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
      command -v "$t" >/dev/null 2>&1 || { echo "FATAL: baked tool missing: $t" >&2; exit 1; }; \
    done

# 7. npm tier (NodeSource node; upstream prefix /usr/local)
RUN npm install -g --prefix /usr/local \
    typescript prettier @ast-grep/cli yarn pnpm gulp-cli tailwindcss \
    vite webpack

# 8. PHP tooling: composer ships in the apt tier (uses php8.4-cli); wp-cli
# and php-cs-fixer via their official phars. One php (8.4) is the baseline;
# multi-version users go mise (documented in PACKAGES.md).
RUN curl -fsSL https://raw.githubusercontent.com/wp-cli/builds/gh-pages/phar/wp-cli.phar -o /usr/local/bin/wp && \
    chmod +x /usr/local/bin/wp && \
    curl -fsSL https://cs.symfony.com/download/php-cs-fixer-v3.phar -o /usr/local/bin/php-cs-fixer && \
    chmod +x /usr/local/bin/php-cs-fixer && \
    php -v | head -1 && wp --version --allow-root && php-cs-fixer --version

# 9. Vendor-script installs: uv (astral) and golangci-lint (official
# install script), plus the golangci-lint LSP via go install.
RUN curl -LsSf https://astral.sh/uv/install.sh | UV_INSTALL_DIR=/usr/local/bin INSTALLER_NO_MODIFY_PATH=1 sh && \
    curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b /usr/local/bin v2.13.2 && \
    GOBIN=/usr/local/bin /usr/local/go/bin/go install github.com/nametake/golangci-lint-langserver@latest && \
    uv --version && golangci-lint --version | head -1

# 10. Bake remaining dev tooling to SYSTEM paths (root-owned).
# Ruby gems -> apt ruby's site dir; Python tools via pipx -> /usr/local/bin
# (keeps user pipx overrides in $HOME/.local/bin ahead of it); bun binary ->
# /usr/local/bun with runtime globals staying in ~/.bun (user tier); asdf
# binary baked, its data dir initializes in-home at runtime (cheap, no
# network). mise itself is installed in step 6.
USER root
RUN gem install jekyll && \
    PIPX_HOME=/usr/local/pipx PIPX_BIN_DIR=/usr/local/bin pipx install litellm && \
    curl -fsSL https://bun.sh/install | BUN_INSTALL=/usr/local/bun bash && \
    ln -sf /usr/local/bun/bin/bun /usr/local/bin/bun && \
    ln -sf /usr/local/bun/bin/bunx /usr/local/bin/bunx && \
    BUN_INSTALL=/usr/local/bun /usr/local/bin/bun add -g @tobilu/qmd && \
    ln -sf /usr/local/bun/bin/qmd /usr/local/bin/qmd && \
    git clone --depth 1 https://github.com/asdf-vm/asdf.git /usr/local/asdf && \
    ln -sf /usr/local/asdf/bin/asdf /usr/local/bin/asdf

# Copy entrypoint scripts (still USER root from step 3b)
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
COPY update-all.sh /usr/local/bin/update-all.sh
COPY network-filter.sh /usr/local/bin/network-filter.sh
COPY clipper /usr/local/bin/clipper
COPY clipboard-x11-sync.sh /usr/local/bin/clipboard-x11-sync.sh
COPY osascript /usr/local/bin/osascript
COPY construct-host-exec /usr/local/bin/construct-host-exec
# We don't COPY install_user_packages.sh here because it is generated at runtime
# and mounted via docker-compose.override.yml.
RUN chmod +x /usr/local/bin/entrypoint.sh /usr/local/bin/update-all.sh /usr/local/bin/network-filter.sh /usr/local/bin/clipper /usr/local/bin/clipboard-x11-sync.sh /usr/local/bin/osascript /usr/local/bin/construct-host-exec

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

# 6. Bake core agents (LAST: most frequently updated layer)
# Each installs via its official installer into a throwaway HOME, then the
# binary is placed at /usr/local/bin — root-owned and OUTSIDE the home bind,
# so bind-tier copies shadow it by PATH (the override contract).
RUN HOME=/tmp/agent-install && mkdir -p "$HOME" && \
    curl -fsSL https://claude.ai/install.sh | bash && \
    install -m0755 "$(find "$HOME" \( -type f -o -type l \) -name claude | sort | head -1)" /usr/local/bin/claude && \
    curl -fsSL https://antigravity.google/cli/install.sh | bash && \
    install -m0755 "$(find "$HOME" \( -type f -o -type l \) -name agy | sort | head -1)" /usr/local/bin/agy && \
    curl -fsSL https://opencode.ai/install | bash && \
    install -m0755 "$(find "$HOME" \( -type f -o -type l \) -name opencode | sort | head -1)" /usr/local/bin/opencode && \
    npm install -g --prefix /usr/local @openai/codex && \
    npm install -g --prefix /usr/local @earendil-works/pi-coding-agent && \
    rm -rf "$HOME" && \
    for c in claude agy opencode codex pi; do "$c" --version >/dev/null 2>&1 || { echo "agent failed execution: $c" >&2; exit 1; }; done

# Switch back to construct user
# USER construct -> Commented out to allow entrypoint to run as root first (for permission fixing)

# 5. Environment Setup
# Set default working directory
WORKDIR /projects
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["/bin/bash"]
