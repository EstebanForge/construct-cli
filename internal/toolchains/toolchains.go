// Package toolchains implements project marker detection and modular toolchain resolution for Construct v3.0.
package toolchains

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ToolchainID identifies a modular toolchain package.
type ToolchainID string

// Supported modular toolchain identifiers.
const (
	ToolchainNode   ToolchainID = "node"
	ToolchainRust   ToolchainID = "rust"
	ToolchainGo     ToolchainID = "go"
	ToolchainPython ToolchainID = "python"
	ToolchainRuby   ToolchainID = "ruby"
	ToolchainPHP    ToolchainID = "php"
	ToolchainJava   ToolchainID = "java"
	ToolchainKotlin ToolchainID = "kotlin"
	ToolchainDart   ToolchainID = "dart"
	ToolchainSwift  ToolchainID = "swift"
)

// AllToolchains enumerates all known supported modular toolchains.
var AllToolchains = []ToolchainID{
	ToolchainNode, ToolchainRust, ToolchainGo, ToolchainPython, ToolchainRuby, ToolchainPHP,
	ToolchainJava, ToolchainKotlin, ToolchainDart, ToolchainSwift,
}

// Marker describes a filesystem file or directory indicator for a toolchain.
type Marker struct {
	Toolchain ToolchainID
	Files     []string
}

// DefaultMarkers maps language ecosystem files to toolchains.
var DefaultMarkers = []Marker{
	{Toolchain: ToolchainNode, Files: []string{"package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lockb", "bun.lock"}},
	{Toolchain: ToolchainRust, Files: []string{"Cargo.toml", "Cargo.lock"}},
	{Toolchain: ToolchainGo, Files: []string{"go.mod", "go.sum", "go.work"}},
	{Toolchain: ToolchainPython, Files: []string{"pyproject.toml", "requirements.txt", "Pipfile", "poetry.lock", "setup.py"}},
	{Toolchain: ToolchainRuby, Files: []string{"Gemfile", "Gemfile.lock"}},
	{Toolchain: ToolchainPHP, Files: []string{"composer.json", "composer.lock"}},
	{Toolchain: ToolchainJava, Files: []string{"pom.xml", "build.gradle", "settings.gradle", "build.gradle.kts", "settings.gradle.kts"}},
	{Toolchain: ToolchainKotlin, Files: []string{"build.gradle.kts", "settings.gradle.kts"}},
	{Toolchain: ToolchainDart, Files: []string{"pubspec.yaml", "pubspec.lock"}},
	{Toolchain: ToolchainSwift, Files: []string{"Package.swift", "Package.resolved"}},
}

// DetectToolchains scans the given root directory and returns the list of detected toolchains.
func DetectToolchains(root string) []ToolchainID {
	if root == "" {
		return nil
	}

	detected := make(map[ToolchainID]bool)
	for _, marker := range DefaultMarkers {
		for _, file := range marker.Files {
			p := filepath.Join(root, file)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				detected[marker.Toolchain] = true
				break
			}
		}
	}

	out := make([]ToolchainID, 0, len(detected))
	for _, id := range AllToolchains {
		if detected[id] {
			out = append(out, id)
		}
	}
	return out
}

// ToolchainMount represents a host-to-guest read-only bind mount for a toolchain.
type ToolchainMount struct {
	ID        ToolchainID
	HostPath  string
	GuestPath string
}

// CacheDir returns the host cache directory for modular toolchains.
func CacheDir() string {
	cacheHome := os.Getenv("XDG_CACHE_HOME")
	if cacheHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		cacheHome = filepath.Join(home, ".cache")
	}
	return filepath.Join(cacheHome, "construct", "toolchains")
}

// DistributionArtifact returns the default GHCR OCI artifact reference for a toolchain.
func DistributionArtifact(id ToolchainID) string {
	return fmt.Sprintf("ghcr.io/estebanforge/construct-toolchain-%s:latest", id)
}

// ResolveMounts computes the required mount definitions for the detected toolchains.
func ResolveMounts(cacheDir string, toolchains []ToolchainID) []ToolchainMount {
	mounts := make([]ToolchainMount, 0, len(toolchains))
	for _, tc := range toolchains {
		hostPath := filepath.Join(cacheDir, string(tc))
		guestPath := filepath.Join("/opt/toolchains", string(tc))
		mounts = append(mounts, ToolchainMount{
			ID:        tc,
			HostPath:  hostPath,
			GuestPath: guestPath,
		})
	}
	return mounts
}

// ResolveExistingMounts returns mounts for detected toolchains whose directories exist on the host.
func ResolveExistingMounts(cacheDir string, toolchains []ToolchainID) []ToolchainMount {
	all := ResolveMounts(cacheDir, toolchains)
	var existing []ToolchainMount
	for _, m := range all {
		if fi, err := os.Stat(m.HostPath); err == nil && fi.IsDir() {
			existing = append(existing, m)
		}
	}
	return existing
}

// AvailableToolchains inspects the host cache directory and returns mounts for all installed toolchains.
func AvailableToolchains(cacheDir string) []ToolchainMount {
	if cacheDir == "" {
		return nil
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return nil
	}
	var mounts []ToolchainMount
	for _, entry := range entries {
		if entry.IsDir() {
			name := entry.Name()
			mounts = append(mounts, ToolchainMount{
				ID:        ToolchainID(name),
				HostPath:  filepath.Join(cacheDir, name),
				GuestPath: filepath.Join("/opt/toolchains", name),
			})
		}
	}
	return mounts
}

// BuildToolchainPath appends the binary directories of active toolchains to the front of PATH.
func BuildToolchainPath(basePath string, toolchains []ToolchainID) string {
	if len(toolchains) == 0 {
		return basePath
	}

	var prefixes []string
	for _, tc := range toolchains {
		binDir := filepath.Join("/opt/toolchains", string(tc), "bin")
		prefixes = append(prefixes, binDir)
	}

	return strings.Join(append(prefixes, basePath), ":")
}
