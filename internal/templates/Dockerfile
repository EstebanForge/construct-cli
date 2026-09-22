# syntax=docker/dockerfile:1
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
    vim tmux nano && \
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

# 2. Install Homebrew for Linux
# Why Homebrew instead of relying solely on APT?
#   1. Up-to-date packages: Debian stable/testing often lags behind latest versions;
#      Homebrew provides bleeding-edge tools critical for modern development workflows.
#   2. Sudoless installation: User-space package management enables automatic, configuration-free
#      setup without requiring root privileges - making it safe and easy for newcomers.
#   3. Binary bottles: Pre-compiled packages download and install in seconds vs minutes of compilation.
#   4. Cross-platform consistency: Same package names and versions work identically on Linux and macOS,
#      simplifying multi-platform development and documentation.
#   5. Larger ecosystem: Many modern CLI tools, language runtimes, and dev utilities are available
#      in Homebrew but missing or severely outdated in Debian repositories.
#   6. No system pollution: Everything installs to /home/linuxbrew, keeping the base system clean
#      and avoiding conflicts with system packages or other containerized environments.
#   7. Easy version management: Simple syntax for pinning versions (package@version) or using latest,
#      with straightforward rollback capabilities if needed.
#
# Follow official installer pattern: create directory as root, clone, then hand over to user
RUN mkdir -p /home/linuxbrew && \
    git clone --depth 1 https://github.com/Homebrew/brew /home/linuxbrew/.linuxbrew && \
    chown -R construct:construct /home/linuxbrew

# Create construct user's .local directory with proper ownership
RUN mkdir -p /home/construct/.local/{bin,lib,node_modules,share} && \
    chown -R construct:construct /home/construct/.local

USER construct
WORKDIR /home/construct

# Set up Homebrew environment (run as construct user)
RUN eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)" && \
    /home/linuxbrew/.linuxbrew/bin/brew --version
# Prevent Homebrew from auto-updating and compiling Python
ENV HOMEBREW_NO_AUTO_UPDATE=1
ENV HOMEBREW_NO_ENV_HINTS=1
# Use standard Linux Homebrew paths for bottle compatibility
ENV HOMEBREW_CELLAR=/home/linuxbrew/.linuxbrew/Cellar
ENV HOMEBREW_PREFIX=/home/linuxbrew/.linuxbrew
# NOTE: Keep this PATH list in sync with:
# - internal/env/env.go (BuildConstructPath)
# - internal/templates/entrypoint.sh
# - internal/templates/docker-compose.yml
ENV PATH="/home/linuxbrew/.linuxbrew/bin:/home/linuxbrew/.linuxbrew/sbin:/home/construct/.local/bin:/home/construct/.npm-global/bin:/home/construct/.cargo/bin:/home/construct/.bun/bin:/home/construct/.opencode/bin:/home/construct/.asdf/bin:/home/construct/.asdf/shims:/home/construct/.local/share/mise/bin:/home/construct/.local/share/mise/shims:/home/construct/.volta/bin:/home/construct/.local/share/pnpm:/home/construct/.yarn/bin:/home/construct/.config/yarn/global/node_modules/.bin:/home/construct/go/bin:/home/construct/.composer/vendor/bin:/home/construct/.config/composer/vendor/bin:/home/construct/.nix-profile/bin:/nix/var/nix/profiles/default/bin:/home/construct/.phpbrew/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
# Force node-gyp to use python3.12 to avoid compat issues with 3.13/3.14
ENV PYTHON=/home/linuxbrew/.linuxbrew/bin/python3.12
# Configure Puppeteer to use system Chromium (prevents arch mismatch on arm64)
ENV PUPPETEER_SKIP_CHROMIUM_DOWNLOAD=true
ENV PUPPETEER_EXECUTABLE_PATH=/usr/bin/chromium

# 3. Bake the packages.toml dev baseline (brew tier)
# Source of truth for this list: internal/templates/packages.toml [brew]
# (2026-09-21 snapshot, deduped). Baked binaries land in /home/linuxbrew
# (PATH tier 1, OUTSIDE the home bind — /home/construct content is shadowed
# by the persistent bind and must never hold baked state).
# Network cost hits the BUILD only; guests get block-IO pulls.
# Baked brew baseline: SPLIT INTO MULTIPLE LAYERS - msb rejects any single
# layer over 10 GiB on `msb load`, and one monolithic brew layer exceeds it.
# Chunk order: stable/large first, fast-moving last. Each layer verifies its
# kegs (brew install has been observed exiting 1 with no error text even
# when every keg poured - tolerate the exit code, fail visibly on kegs).
RUN --mount=type=cache,target=/home/construct/.cache/Homebrew,sharing=locked,uid=1000,gid=1000 \
    eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)" && \
    brew tap shivammathur/extensions && \
    brew tap shivammathur/php && \
    brew tap EstebanForge/tap && \
    brew trust shivammathur/extensions && \
    brew trust shivammathur/php && \
    brew trust EstebanForge/tap && \
    (brew install \
      ast-grep \
      yq \
      sd \
      fzf \
      eza \
      fd \
      bat \
      ripgrep \
      jq \
      curl \
      wget \
      tree \
      unzip \
      socat \
      make \
      htop \
      netcat \
      bind \
      sshpass \
      nano \
      lftp \
      tmux \
      btop \
      imagemagick \
      topgrade \
      libgit2 \
      gh \
      git-lfs \
      git-delta \
      git-cliff \
      rsync \
      procs \
      python-setuptools \
      awscli \
      cmake \
      pkg-config \
      direnv \
      gum \
      rtk) || \
      echo "⚠️ brew install exited non-zero; verifying kegs"; \
    for f in ast-grep \
      yq \
      sd \
      fzf \
      eza \
      fd \
      bat \
      ripgrep \
      jq \
      curl \
      wget \
      tree \
      unzip \
      socat \
      make \
      htop \
      netcat \
      bind \
      sshpass \
      nano \
      lftp \
      tmux \
      btop \
      imagemagick \
      topgrade \
      libgit2 \
      gh \
      git-lfs \
      git-delta \
      git-cliff \
      rsync \
      procs \
      python-setuptools \
      awscli \
      cmake \
      pkg-config \
      direnv \
      gum \
      rtk; do \
      brew list --versions "$f" >/dev/null 2>&1 || { echo "FATAL: keg missing: $f" >&2; exit 1; }; \
    done


RUN --mount=type=cache,target=/home/construct/.cache/Homebrew,sharing=locked,uid=1000,gid=1000 \
    eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)" && \
    (brew install \
      go \
      openjdk \
      typescript \
      rust \
      kotlin \
      lua \
      ruby) || \
      echo "⚠️ brew install exited non-zero; verifying kegs"; \
    for f in go \
      openjdk \
      typescript \
      rust \
      kotlin \
      lua \
      ruby; do \
      brew list --versions "$f" >/dev/null 2>&1 || { echo "FATAL: keg missing: $f" >&2; exit 1; }; \
    done


# dart-sdk in its own small layer. swift, zig, erlang, elixir, gleam and
# the graphics chain they pulled (wxwidgets/gtk+3/mesa -> unversioned llvm)
# were REMOVED 2026-09-22 for image size (owner decision; ~8.4 GiB of
# Cellar weight) - users restore them via packages.toml [brew] entries
# (docs/PACKAGES.md).
RUN --mount=type=cache,target=/home/construct/.cache/Homebrew,sharing=locked,uid=1000,gid=1000 \
    eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)" && \
    (brew install \
      dart-sdk \
      perl \
      gnucobol) || \
      echo "⚠️ brew install exited non-zero; verifying kegs"; \
    for f in dart-sdk \
      perl \
      gnucobol; do \
      brew list --versions "$f" >/dev/null 2>&1 || { echo "FATAL: keg missing: $f" >&2; exit 1; }; \
    done


RUN --mount=type=cache,target=/home/construct/.cache/Homebrew,sharing=locked,uid=1000,gid=1000 \
    eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)" && \
    (brew install \
      node \
      python@3.12 \
      python@3 \
      openssl@3 \
      gcc \
      nasm \
      yasm) || \
      echo "⚠️ brew install exited non-zero; verifying kegs"; \
    for f in node \
      python@3.12 \
      python@3 \
      openssl@3 \
      gcc \
      nasm \
      yasm; do \
      brew list --versions "$f" >/dev/null 2>&1 || { echo "FATAL: keg missing: $f" >&2; exit 1; }; \
    done




RUN --mount=type=cache,target=/home/construct/.cache/Homebrew,sharing=locked,uid=1000,gid=1000 \
    eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)" && \
    (brew install \
      postgresql@16 \
      mysql-client \
      redis \
      sqlite \
      podman \
      podman-compose \
      groovy \
      scala \
      r \
      micropython \
      ocaml \
      swi-prolog) || \
      echo "⚠️ brew install exited non-zero; verifying kegs"; \
    for f in postgresql@16 \
      mysql-client \
      redis \
      sqlite \
      podman \
      podman-compose \
      groovy \
      scala \
      r \
      micropython \
      ocaml \
      swi-prolog; do \
      brew list --versions "$f" >/dev/null 2>&1 || { echo "FATAL: keg missing: $f" >&2; exit 1; }; \
    done


RUN --mount=type=cache,target=/home/construct/.cache/Homebrew,sharing=locked,uid=1000,gid=1000 \
    eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)" && \
    (brew install \
      golangci-lint \
      golangci-lint-langserver \
      php-cs-fixer \
      prettier \
      shellcheck \
      yamllint \
      yarn \
      pnpm \
      composer \
      neovim \
      gulp-cli \
      ffmpeg \
      wp-cli \
      tailwindcss \
      uv \
      pipx \
      vite \
      webpack \
      tlrc \
      ninja \
      gradle \
      fastmod \
      hugo \
      zola \
      EstebanForge/tap/mcp-cli-ent \
      EstebanForge/tap/md-over-here) || \
      echo "⚠️ brew install exited non-zero; verifying kegs"; \
    for f in golangci-lint \
      golangci-lint-langserver \
      php-cs-fixer \
      prettier \
      shellcheck \
      yamllint \
      yarn \
      pnpm \
      composer \
      neovim \
      gulp-cli \
      ffmpeg \
      wp-cli \
      tailwindcss \
      uv \
      pipx \
      vite \
      webpack \
      tlrc \
      ninja \
      gradle \
      fastmod \
      hugo \
      zola \
      EstebanForge/tap/mcp-cli-ent \
      EstebanForge/tap/md-over-here; do \
      brew list --versions "$f" >/dev/null 2>&1 || { echo "FATAL: keg missing: $f" >&2; exit 1; }; \
    done


RUN --mount=type=cache,target=/home/construct/.cache/Homebrew,sharing=locked,uid=1000,gid=1000 \
    eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)" && \
    for v in 8.2 8.3 8.4 8.5 8.6; do \
      brew install "shivammathur/php/php@$v" || true; \
      brew list --versions "php@$v" >/dev/null 2>&1 || { echo "FATAL: keg missing: php@$v" >&2; exit 1; }; \
      brew install "shivammathur/extensions/pcov@$v" || true; \
      brew list --versions "pcov@$v" >/dev/null 2>&1 || { echo "FATAL: keg missing: pcov@$v" >&2; exit 1; }; \
      brew unlink "shivammathur/php/php@$v" || true; \
    done && \
    # Exactly ONE version stays linked (default php for composer/wp-cli/
    # php-cs-fixer); switchers manage the rest at runtime.
    brew link --overwrite "shivammathur/php/php@8.4" || true

# 3b. Bake remaining dev tooling to SYSTEM paths (root-owned).
# Ruby gems -> brew ruby's site dir (tier-1 bin); Python tools via pipx ->
# /usr/local/bin (tier 3, keeps user pipx overrides in $HOME/.local/bin
# ahead of it); bun binary -> /usr/local/bun with runtime globals staying
# in ~/.bun (user tier); mise/asdf binaries baked, their data dirs
# initialize in-home at runtime (cheap, no network).
USER root
RUN eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)" && \
    gem install jekyll && \
    PIPX_HOME=/usr/local/pipx PIPX_BIN_DIR=/usr/local/bin pipx install litellm && \
    curl -fsSL https://bun.sh/install | BUN_INSTALL=/usr/local/bun bash && \
    ln -sf /usr/local/bun/bin/bun /usr/local/bin/bun && \
    ln -sf /usr/local/bun/bin/bunx /usr/local/bin/bunx && \
    BUN_INSTALL=/usr/local/bun /usr/local/bin/bun add -g @tobilu/qmd && \
    ln -sf /usr/local/bun/bin/qmd /usr/local/bin/qmd && \
    curl -fsSL https://mise.run | MISE_INSTALL_PATH=/usr/local/bin/mise sh && \
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
# npm installs force --prefix /usr/local: brew node's default prefix would
# land them in tier-1 /home/linuxbrew, breaking the tier order.
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
