# Spec: Rename Internal Container Mount Point

## Goal
Rename the internal container mount point and working directory from `/app` to `/workspace` to better reflect that the sandbox is a general-purpose environment, not just for "apps".

## Scope
- Update `internal/templates/Dockerfile` to set `WORKDIR /workspace`.
- Update `internal/templates/docker-compose.yml` to mount `${PWD}` to `/workspace` and set `working_dir: /workspace`.
- Update `internal/runtime/runtime.go` to use `/workspace` in dynamically generated docker-compose overrides.
- Update `internal/templates/osascript` log path.
- Update documentation (`README.md`, `DESIGN.md`) to reflect the change.

## Verification
- Run `construct sys reset` (or equivalent) to rebuild templates.
- Verify that running an agent (e.g., `ct gemini "ls -d /workspace"`) correctly shows the new path and that the current directory is mounted there.
