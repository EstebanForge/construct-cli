# Implementation Plan - User-Defined Packages

## Phase 1: Configuration & Template Structure

- [x] Task: Define `PackagesConfig` Struct and Load Logic
    - Create a new `PackagesConfig` struct in `internal/config/` mirroring the TOML structure (including `[tools]` section for boolean toggles).
    - Update `internal/config/` to load `packages.toml` if it exists.
    - Add unit tests for loading valid, invalid, and missing `packages.toml`.
- [x] Task: Create `packages.toml` Template
    - Add a default `packages.toml` template in `internal/templates/`.
    - Include commented-out examples for `[tools]` (phpbrew, nix, vmr, asdf, mise).
    - Update `config.Init` to create a default `packages.toml` (with commented-out examples) if it doesn't exist.
- [ ] Task: Conductor - User Manual Verification 'Configuration & Template Structure' (Protocol in workflow.md)

## Phase 2: Container Installation Logic (Entrypoint)

- [x] Task: Update `entrypoint.sh` to Read Package Lists
    - Modify `entrypoint.sh` to check for `packages.toml`.
    - Since `entrypoint.sh` is bash, parsing TOML is hard. **Refinement:** The Go CLI should parse `packages.toml` and pass the lists to the container, OR we generate a simple intermediate file (like `packages.env` or similar) that `entrypoint.sh` can source/read easily.
    - **Decision:** The Go CLI will generate a `user_packages.sh` (or similar) into the mounted configuration directory (or a hidden `.local` file) that `entrypoint.sh` can execute.
    - **Implementation:**
        - Create a function in the Go CLI that reads `packages.toml` and generates a bash script `install_user_packages.sh`.
        - This script will contain the `apt-get install ...`, `brew install ...`, etc., commands constructed from the config.
        - **Add logic for Specialized Tools:** If enabled in `[tools]`:
            - `phpbrew`: Download PHAR, chmod, move to `/usr/local/bin`.
            - `nix`: Execute official install script.
            - `vmr`: Execute install script.
            - `asdf`/`mise`: Execute `brew install`.
        - Write this script to `~/.config/construct/container/user_install.sh` (or similar mounted path).
- [x] Task: Modify `entrypoint.sh` to Execute User Script
    - Update `internal/templates/entrypoint.sh` to look for and execute the generated `user_install.sh` if it exists.
    - Ensure it runs with appropriate permissions (e.g., `apt` needs root/sudo, `brew` needs user).
    - Add logging to `entrypoint.sh` for this step.
- [ ] Task: Conductor - User Manual Verification 'Container Installation Logic' (Protocol in workflow.md)

## Phase 3: The `sys` Command for Live Updates

- [x] Task: Implement `sys install-packages` Command
    - Add a new subcommand `install-packages` to the `sys` package/module.
    - This command should:
        1. Reload the configuration to get the latest `packages.toml` content.
        2. Regenerate the `user_install.sh` script.
        3. Execute the `user_install.sh` script inside the running container (using `docker exec` or similar via the runtime manager).
- [x] Task: Wire up `sys` Command in CLI
    - Ensure the command is accessible via the CLI (e.g., `construct sys install-packages`).
- [ ] Task: Conductor - User Manual Verification 'The sys Command for Live Updates' (Protocol in workflow.md)

## Phase 4: Integration & Verification

- [x] Task: End-to-End Test
    - Verify that adding a package to `packages.toml` and restarting works.
    - Verify that adding a package and running `sys install-packages` works.
    - Verify error handling (invalid package names).
- [x] Task: Documentation
    - Update `README.md` or user docs to explain how to use `packages.toml`.
- [x] Task: Conductor - User Manual Verification 'Integration & Verification' (Protocol in workflow.md)
