# Plan: Implement `construct sys agents-md`

## Phase 1: Logic & Discovery
- [x] Task: Define Agent Memory Metadata
    - Create a struct to hold agent name, friendly name, and relative path(s).
    - Implement a helper to get the absolute path (expanding `~` and handling Cline fallbacks).
- [x] Task: Implement `OpenAgentMemory` function in `internal/sys/ops.go`
    - Logic for checking existence, creating directories/files, and invoking the editor.
- [x] Task: Implement `ListAgentMemories` in `internal/sys/ops.go`
    - Display the header.
    - Use `gum choose` to present the list of agents.
    - Invoke `OpenAgentMemory` upon selection.
- [x] Task: Conductor - User Manual Verification 'Phase 1: Logic & Discovery' (Protocol in workflow.md)

## Phase 2: CLI Integration
- [x] Task: Add `agents-md` to `handleSysCommand` in `cmd/construct/main.go`.
- [x] Task: Update help text in `cmd/construct/main.go` and `internal/ui/help.go`.
- [x] Task: Conductor - User Manual Verification 'Phase 2: CLI Integration' (Protocol in workflow.md)

## Phase 3: Testing & Verification
- [x] Task: Write unit tests for path expansion and file creation logic.
- [x] Task: Verify GUI vs Terminal editor behavior.
- [x] Task: Conductor - User Manual Verification 'Phase 3: Testing & Verification' (Protocol in workflow.md)