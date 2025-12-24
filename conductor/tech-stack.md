# Technology Stack

## Core Language & Tools
*   **Go (v1.24):** Primary language for the CLI implementation, chosen for its performance, static typing, and excellent cross-compilation support.
*   **TOML:** Configuration format handled via `github.com/pelletier/go-toml/v2`, providing a human-readable and structured way to manage user settings.

## Containerization & Virtualization
*   **Multi-Runtime Support:** Designed to work seamlessly with:
    *   **Docker:** Standard containerization for Linux and Windows (WSL).
    *   **Podman:** Daemonless container engine alternative.
    *   **macOS Native Containers:** Integration with macOS's native virtualization capabilities.
*   **Base OS:** **Debian Trixie Slim** serves as the base image for the isolated agent environment, chosen for its balance of stability and modern package availability.

## Sandbox Environment (Container)
*   **Package Management:**
    *   **Apt:** Standard Debian package management for core system utilities.
    *   **Homebrew (Linuxbrew):** Used inside the container for flexible and up-to-date tool management.
*   **Pre-loaded Runtimes:**
    *   **Node.js (v24)**
    *   **Python (v3)**
    *   **Bun**
*   **Security & Networking:**
    *   **UFW/Iptables:** Used for implementing network isolation rules.
    *   **Gosu:** For safe user privilege management within the container.

## Platform Support
*   **Host Systems:** macOS, Linux, and Windows (via WSL).
*   **Deployment:** Single-binary distribution for easy installation and portability.
