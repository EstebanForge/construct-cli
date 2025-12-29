# Specification: Dynamic Project Mount Path

## Overview
Currently, the Construct CLI mounts the user's current working directory to a static `/workspace` path inside the container. To improve contextual awareness and long-term memory for agents, this track will implement dynamic mounting where the host's current directory is mounted to `/projects/<folder_name>` and the agent's working directory is set accordingly.

## Functional Requirements
- **Dynamic Path Derivation:** The CLI must determine the name of the current host directory at runtime.
- **New Mount Point:** Instead of `/workspace`, the host directory will be mounted to `/projects/<folder_name>` inside the container.
- **Working Directory:** The container's `working_dir` must be dynamically set to the new mount path.
- **Template Updates:**
    - Update `internal/templates/docker-compose.yml` to use dynamic paths for volumes and working directory.
    - Update `internal/templates/Dockerfile` to set a generic base (likely `/projects`) or handle dynamic `WORKDIR`.
    - Update `internal/templates/osascript` and any other templates referencing `/workspace`.
- **Code Updates:**
    - Modify `internal/runtime/runtime.go` to calculate the project name and inject it into the Docker/Compose configuration.
    - Ensure environment variables or configuration overrides correctly reflect the dynamic path.
- **Validation:** Ensure the system handles folder names with spaces and special characters appropriately for Linux/macOS.

## Non-Functional Requirements
- **Simplicity:** The path should be derived directly from the folder name at call time without requiring additional local configuration files.
- **Consistency:** All internal tools and agents should consistently see the same dynamic path.

## Acceptance Criteria
- When running an agent from a host directory named `project-alpha`, the command `pwd` inside the agent should return `/projects/project-alpha`.
- File system operations (read, write, list) must function correctly within the new dynamic path.
- Existing templates and hardcoded references to `/workspace` must be updated or replaced.

## Out of Scope
- Storing a "sticky" project name in a local configuration file (the path will always reflect the current folder name).
- Support for mounting multiple different project folders simultaneously in a single agent session (out of scope for this specific change).
