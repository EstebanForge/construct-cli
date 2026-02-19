# Specification: User-Defined Packages Configuration

## Overview
Currently, the Construct environment has a hardcoded list of packages in `entrypoint.sh`. Users cannot easily add their own tools without modifying the internal scripts, and these changes are lost when the Construct is updated. This track introduces a `packages.toml` file that allows users to specify additional packages to be installed via `apt`, `brew`, `npm`, and `pip`. Additionally, a new command will be added to the `sys` module to apply these changes on-demand without requiring a full container restart.

## Functional Requirements
- **Configuration File**: Support a new configuration file `packages.toml` located in the Construct configuration directory (alongside `config.toml`).
- **Supported Managers**:
    - **APT**: System-level Debian packages.
    - **Homebrew**: CLI tools and languages, including support for custom `taps`.
    - **NPM**: Global Node.js packages.
    - **PIP**: Python packages (installed via `pip` or `pipx`).
    - **Specialized Tools/Managers**:
        - **PHPBrew**: PHP version manager (installed via PHAR).
        - **Nix**: The Nix package manager (installed via install script).
        - **asdf**: Extendable version manager (installed via Homebrew).
        - **mise**: Polyglot tool version manager (installed via Homebrew).
        - **vmr**: Virtual Version Manager (installed via script).

- **TOML Structure**:
  ```toml
  [apt]
  packages = ["htop", "vim-gtk3"]

  [brew]
  taps = ["common-family/homebrew-tap"]
  packages = ["fastlane"]

  [npm]
  packages = ["typescript-language-server"]

  [pip]
  packages = ["black", "isort"]

  [tools]
  # Specialized tools to install and configure
  phpbrew = true
  nix = true
  vmr = true
  asdf = true
  mise = true
  ```
- **Installation Logic**:
    - The Construct CLI must detect and validate `packages.toml`.
    - The package lists must be passed to the container environment.
    - `entrypoint.sh` must be updated to process these lists and perform the installations during the setup/update phase.
    - Homebrew taps must be tapped before attempting to install packages from them.
- **On-Demand Installation (Sys Command)**:
    - A new command `sys packages --install` (or similar) will be added to the `sys` module.
    - This command will trigger the installation logic for the packages defined in `packages.toml` within the running container.
    - It ensures that adding a package doesn't require a full container restart.
- **Persistence**: Since the Construct environment is designed to be reproducible, these packages should be re-installed whenever the setup script runs (e.g., after an update or on first boot).

## Non-Functional Requirements
- **Idempotency**: Installation commands should be safe to run multiple times (package managers generally handle this, but the script should ensure it).
- **Graceful Failure**: If a user-defined package fails to install, the setup should log a warning but continue with other packages/steps.
- **Performance**: The addition of user packages should not significantly delay the startup process if the `packages.toml` hasn't changed.

## Acceptance Criteria
1.  Creating `packages.toml` with a list of packages results in those packages being available inside the Construct after start/restart.
2.  Adding a Homebrew tap in `packages.toml` allows installing packages from that tap.
3.  The Construct starts successfully even if `packages.toml` is missing or empty.
4.  Invalid TOML in `packages.toml` is reported as an error by the CLI before starting the container.
5.  Running the new `sys` command successfully installs newly added packages from `packages.toml` into the running container without a restart.

## Out of Scope
- Support for package managers not mentioned above (e.g., Cargo, RubyGems).
- Removal of packages (packages will be cleared when the image is rebuilt/updated).
- Version pinning for all managers (users can specify versions in the package name string if the manager supports it, e.g., `npm install -g pkg@1.2.3`).
