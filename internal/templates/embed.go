// Package templates provides embedded template files for Docker, Compose, and configuration.
package templates

import _ "embed"

// Dockerfile is the embedded content of the Dockerfile template.
//
//go:embed Dockerfile
var Dockerfile string

// DockerCompose is the embedded content of the docker-compose.yml template.
//
//go:embed docker-compose.yml
var DockerCompose string

// Config is the embedded content of the default config.toml template.
//
//go:embed config.toml
var Config string

// Packages is the embedded content of the default packages.toml template.
//
//go:embed packages.toml
var Packages string

// Entrypoint is the embedded content of the entrypoint.sh script.
//
//go:embed entrypoint.sh
var Entrypoint string

// EntrypointHash is the embedded content of the entrypoint hash helper script.
//
//go:embed entrypoint-hash.sh
var EntrypointHash string

// UpdateAll is the embedded content of the update-all.sh script.
//
//go:embed update-all.sh
var UpdateAll string

// AgentPatch is the embedded content of the agent patching script.
//
//go:embed agent-patch.sh
var AgentPatch string

// NetworkFilter is the embedded content of the network-filter.sh script.
//
//go:embed network-filter.sh
var NetworkFilter string

// Clipper is the embedded content of the clipper script.
//
//go:embed clipper
var Clipper string

// ClipboardX11Sync is the embedded content of the clipboard X11 sync script.
//
//go:embed clipboard-x11-sync.sh
var ClipboardX11Sync string

// Osascript is the embedded content of the osascript shim script.
//
//go:embed osascript
var Osascript string

// PowershellExe is the embedded content of the fake powershell.exe for codex WSL fallback.
//
//go:embed powershell.exe
var PowershellExe string
