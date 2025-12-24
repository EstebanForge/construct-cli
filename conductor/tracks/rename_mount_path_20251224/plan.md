# Plan: Rename Internal Container Mount Point

## Phase 1: Code and Template Updates [checkpoint: 23168ca]
- [x] Task: Update `internal/templates/Dockerfile` `WORKDIR` from `/app` to `/workspace`. b6711f2
- [x] Task: Update `internal/templates/docker-compose.yml` mounts and `working_dir` to `/workspace`. 8423ffc
- [x] Task: Update `internal/runtime/runtime.go` to replace `/app` with `/workspace` in compose override generation. 51d3965
- [x] Task: Update `internal/templates/osascript` to use `/workspace/osascript_debug.log`. 95576bb
- [x] Task: Conductor - User Manual Verification 'Phase 1: Code and Template Updates' (Protocol in workflow.md) 23168ca

## Phase 2: Documentation Updates
- [ ] Task: Update `DESIGN.md` to reflect the move from `/app` to `/workspace`.
- [ ] Task: Update `README.md` to reflect the move from `/app` to `/workspace`.
- [ ] Task: Conductor - User Manual Verification 'Phase 2: Documentation Updates' (Protocol in workflow.md)

## Phase 3: Verification
- [ ] Task: Verify the change by building and running the CLI, checking that `$PWD` is correctly mounted to `/workspace` inside the container.
- [ ] Task: Conductor - User Manual Verification 'Phase 3: Verification' (Protocol in workflow.md)
