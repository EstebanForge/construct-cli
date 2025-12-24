# Specification: `construct sys agents-md`

## Overview
Add a new system command `construct sys agents-md` to allow users to manage global instruction files (memories) for various CLI agents. This command provides a unified interface to discover, initialize, and edit these files across different agent ecosystems.

## Functional Requirements
1.  **Command Name**: `construct sys agents-md`.
2.  **Supported Agents & Paths**:
    -   **Gemini CLI**: `~/.gemini/GEMINI.md`
    -   **Qwen CLI**: `~/.qwen/AGENTS.md`
    -   **OpenCode CLI**: `~/.config/opencode/AGENTS.md`
    -   **Claude CLI**: `~/.claude/CLAUDE.md`
    -   **Codex CLI**: `~/.codex/AGENTS.md`
    -   **Copilot CLI**: `~/.copilot/AGENTS.md`
    -   **Cline CLI**: `~/Documents/Cline/Rules/AGENTS.md` (Primary) or `~/Cline/Rules/AGENTS.md` (Fallback)
3.  **UI Header**:
    -   Before displaying the selection list, show a clear header:
        > "These are the main AGENTS.md files used by each agent. Read more about agent instructions in AGENTS.md. Select an agent to edit its memory file; it will open in your default editor."
4.  **Interactive Selection**:
    -   Present the full list of supported agents using `gum choose`.
    -   **Format**: `Friendly Name (~/relative/path)` (e.g., `Gemini CLI (~/.gemini/GEMINI.md)`).
5.  **File Management (On-Demand)**:
    -   Upon selection, check if the file exists.
    -   **Cline Fallback Logic**: For Cline, if the primary path doesn't exist but the fallback does, use the fallback. If neither exists, create the primary path.
    -   If the file **does not exist**, create it and any necessary parent directories (`os.MkdirAll`).
    -   Initialize new files as empty markdown files.
6.  **Editor Integration**:
    -   Open the selected (and potentially just created) file using the system's preferred editor logic as implemented in `OpenConfig`.

## Non-Functional Requirements
-   **UX**: Informative and professional header. Selection "just works" by creating missing files.
-   **Reliability**: Safe path expansion and directory creation.

## Acceptance Criteria
-   `construct sys agents-md` displays the required header.
-   The selection list shows all 7 supported agents.
-   Selecting an agent correctly expands the `~` path.
-   Missing files and directories are automatically created.
-   The editor opens the correct file path.