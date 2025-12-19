FROM debian:trixie-slim

# 1. Create non-root user and install system packages
RUN useradd -m -s /bin/bash construct && \
    apt-get update && apt-get install -y \
    curl git procps file build-essential \
    vim nano python3 python3-pip python3-venv python3-dev pipx \
    fd-find ripgrep jq bat tmux sudo \
    ufw iptables dnsutils \
    xclip xsel wl-clipboard imagemagick && \
    rm -rf /var/lib/apt/lists/*

# Allow construct user to run privileged commands without password
RUN echo "construct ALL=(ALL) NOPASSWD: /usr/bin/apt-get, /usr/bin/apt, /usr/sbin/ufw" >> /etc/sudoers.d/construct && \
    chmod 0440 /etc/sudoers.d/construct

# 2. Install Homebrew (lightweight, for tools not in Debian repos or too outdated in Debian)
# Follow official installer pattern: create directory as root, clone, then hand over to user
RUN mkdir -p /home/linuxbrew && \
    git clone https://github.com/Homebrew/brew /home/linuxbrew/.linuxbrew && \
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
ENV HOMEBREW_NO_INSTALL_FROM_API=1
ENV HOMEBREW_NO_ENV_HINTS=1
# Use standard Linux Homebrew paths for bottle compatibility
ENV HOMEBREW_CELLAR=/home/linuxbrew/.linuxbrew/Cellar
ENV HOMEBREW_PREFIX=/home/linuxbrew/.linuxbrew
ENV PATH="/home/linuxbrew/.linuxbrew/bin:/home/construct/.local/bin:${PATH}"
# Force node-gyp to use python3.12 to avoid compat issues with 3.13/3.14
ENV PYTHON=/home/linuxbrew/.linuxbrew/bin/python3.12

# 3. Install essential runtime dependencies only
# Note: CLI tools will be installed via entrypoint.sh to ensure proper PATH setup
RUN eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)" && \
    brew install node@24 python@3 oven-sh/bun/bun || true

# 4. Switch to root to copy scripts to /usr/local/bin
USER root

# Copy entrypoint scripts
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
COPY update-all.sh /usr/local/bin/update-all.sh
COPY network-filter.sh /usr/local/bin/network-filter.sh
COPY clipper /usr/local/bin/clipper
RUN chmod +x /usr/local/bin/entrypoint.sh /usr/local/bin/update-all.sh /usr/local/bin/network-filter.sh /usr/local/bin/clipper

# Switch back to construct user
USER construct

# 5. Environment Setup
WORKDIR /app
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["/bin/bash"]
