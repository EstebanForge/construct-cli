// Package constants defines application-wide constants.
package constants

// AppName and related constants define CLI identity, paths, and URLs.
const (
	AppName         = "construct"
	ConfigDir       = ".config/construct-cli"
	ImageName       = "construct-box"
	Version         = "1.2.10"
	GithubAPIURL    = "https://api.github.com/repos/EstebanForge/construct-cli/releases/latest"
	GithubRawURL    = "https://raw.githubusercontent.com/EstebanForge/construct-cli/main/VERSION"
	UpdateCheckFile = "last-update-check"
	GithubRepo      = "EstebanForge/construct-cli"
)

// FileBasedPasteAgents lists agents that use file-based image paste (path reference)
// instead of raw bytes. These agents receive "@path/to/image.png" instead of binary data.
const FileBasedPasteAgents = "gemini,qwen,codex"
