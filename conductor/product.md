# Initial Concept
The Construct is a single-binary CLI that boots a clean and isolated container, preloaded with AI agents. It keeps your host free of dependency sprawl, adds optional network isolation, and works with Docker, Podman, or macOS native container runtime.

# Product Guide

## 1. Target Users
*   **Security-conscious developers:** Professionals who prioritize the integrity of their host machines and require strict isolation for running third-party LLM agents.
*   **AI engineers and researchers:** Individuals experimenting with various agentic frameworks who need a stable, reproducible environment for testing and development.
*   **DevOps engineers:** Teams looking for a consistent, containerized environment to deploy and manage AI tools across different infrastructure.

## 2. Core Goals
*   **Secure Sandboxing:** Provide a robust, isolated environment that prevents LLM agents from accessing or damaging the host system's sensitive data or configuration.
*   **Simplified Deployment:** Streamline the process of installing, configuring, and running a diverse range of AI agents (Claude, Gemini, Qwen, etc.) without complex setup procedures.
*   **Seamless Developer Experience:** Ensure the tool works "out of the box" across major operating systems (macOS, Linux, Windows/WSL) with zero configuration required for standard use cases.

## 3. Key Features
*   **Path-Locked Isolation:** Agents are restricted to a dedicated `/workspace` directory, preventing unauthorized file system access and ensuring a clean, context-aware environment.
*   **Unified Clipboard Bridge:** A seamless link between the host and container clipboards, supporting both text and image pasting for a smooth workflow.
*   **Granular Network Control:** Configurable network isolation modes (Strict, Permissive, Offline) to manage agent internet access and protect against exfiltration.
*   **Automatic Runtime Management:** Intelligent detection and utilization of available container runtimes (Docker, Podman, Native) for optimal performance.

## 4. Interaction Model
*   **Unified CLI:** A single command-line interface (`ct` or `construct`) serves as the primary entry point for launching agents, managing configurations, and performing system maintenance.
*   **Future TUI:** Plans to introduce an interactive Text User Interface (TUI) to simplify complex configuration tasks and agent selection.

## 5. Integration Philosophy
*   **Agnostic & Flexible:** The platform is designed to be a general-purpose sandbox capable of running any CLI tool. While it currently features a curated list of agents, the architecture is extensible to support future CLI agents as they emerge in the market.
