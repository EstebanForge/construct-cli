# Plan: Dynamic Project Mount Path

## Phase 1: Preparation & Design [~]
- [x] Task: Audit codebase for all hardcoded references to `/workspace`. 63b8d9c
    - [x] Sub-task: Search `internal/` for `/workspace`. 63b8d9c
    - [x] Sub-task: Search `cmd/` for `/workspace`. 63b8d9c
    - [x] Sub-task: Search `scripts/` and docs (`README.md`, `DESIGN.md`) for `/workspace`. 63b8d9c
- [x] Task: Design the mechanism for passing the project name/path to the runtime. 23c6f0d
## Phase 2: Template Updates [x]
- [x] Task: Update `internal/templates/docker-compose.yml`. 1b2e3f4
    - [x] Sub-task: Change volume mount to use variable (e.g., `${PROJECT_MOUNT_PATH}`). 1b2e3f4
    - [x] Sub-task: Change `working_dir` to use variable. 1b2e3f4
- [x] Task: Update `internal/templates/Dockerfile`. 5a6b7c8
    - [x] Sub-task: Change `WORKDIR` to a neutral parent (e.g., `/projects`) or ensure it's overridden by Compose. 5a6b7c8
- [x] Task: Update helper scripts/templates. 9d0e1f2
    - [x] Sub-task: Update `internal/templates/osascript` logging path. 9d0e1f2

## Phase 3: Runtime Implementation (TDD) [x]
- [x] Task: Write tests for dynamic path generation in `internal/runtime`. 3f4a5b6
    - [x] Sub-task: Create test case for simple directory name. 3f4a5b6
    - [x] Sub-task: Create test case for directory with spaces/symbols. 3f4a5b6
- [x] Task: Implement dynamic path logic in `internal/runtime/runtime.go`. 7c8d9e0
    - [x] Sub-task: Add function to extract current directory name. 7c8d9e0
    - [x] Sub-task: Modify Compose override generation to inject the correct `PROJECT_MOUNT_PATH` and `working_dir`. 7c8d9e0

## Phase 4: Integration & Verification [x]
- [x] Task: Update `DESIGN.md` and documentation to reflect the path change. 4e5f6a7
- [x] Task: Manual Verification. 8b9c0d1
    - [x] Sub-task: Run agent in a folder named `test-project`. 8b9c0d1
    - [x] Sub-task: Verify `pwd` inside agent returns `/projects/test-project`. 8b9c0d1
    - [x] Sub-task: Verify file access works. 8b9c0d1
- [x] Task: Conductor - User Manual Verification 'Integration & Verification' (Protocol in workflow.md) 2a3b4c5
