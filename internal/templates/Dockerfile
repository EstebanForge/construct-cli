FROM debian:trixie-slim

# 1. Create non-root user and install system packages
RUN useradd -m -s /bin/bash construct && \
    apt-get update && apt-get install -y \
    build-essential git curl wget sudo gosu socat xvfb && \
    rm -rf /var/lib/apt/lists/*

# Allow construct user to run privileged commands without password
RUN echo "construct ALL=(ALL) NOPASSWD: /usr/bin/apt*, /usr/sbin/apt*, /usr/bin/apt-get, /usr/sbin/apt-get, /usr/sbin/ufw" >> /etc/sudoers.d/construct && \
    chmod 0440 /etc/sudoers.d/construct

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
ENV PATH="/home/linuxbrew/.linuxbrew/bin:/home/construct/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
# Force node-gyp to use python3.12 to avoid compat issues with 3.13/3.14
ENV PYTHON=/home/linuxbrew/.linuxbrew/bin/python3.12

# 3. Install essential runtime dependencies only
# Note: CLI tools will be installed via entrypoint.sh to ensure proper PATH setup
RUN eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)" && \
    brew install node python@3 || true

# 4. Switch to root to copy scripts to /usr/local/bin
USER root

# Copy entrypoint scripts
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
COPY update-all.sh /usr/local/bin/update-all.sh
COPY network-filter.sh /usr/local/bin/network-filter.sh
COPY clipper /usr/local/bin/clipper
COPY clipboard-x11-sync.sh /usr/local/bin/clipboard-x11-sync.sh
COPY osascript /usr/local/bin/osascript
COPY powershell.exe /usr/local/bin/powershell.exe
# We don't COPY install_user_packages.sh here because it is generated at runtime
# and mounted via docker-compose.override.yml.
RUN chmod +x /usr/local/bin/entrypoint.sh /usr/local/bin/update-all.sh /usr/local/bin/network-filter.sh /usr/local/bin/clipper /usr/local/bin/clipboard-x11-sync.sh /usr/local/bin/osascript /usr/local/bin/powershell.exe

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

# Switch back to construct user
# USER construct -> Commented out to allow entrypoint to run as root first (for permission fixing)

# 5. Environment Setup
# Set default working directory
WORKDIR /projects
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["/bin/bash"]
