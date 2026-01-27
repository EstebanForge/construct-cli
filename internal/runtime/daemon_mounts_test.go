package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EstebanForge/construct-cli/internal/config"
)

func TestResolveDaemonMountsDisabled(t *testing.T) {
	cfg := &config.Config{}
	result := ResolveDaemonMounts(cfg)
	if result.Enabled {
		t.Fatalf("expected daemon mounts disabled when config flag is false")
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", result.Warnings)
	}
}

func TestResolveDaemonMountsEmptyPaths(t *testing.T) {
	cfg := &config.Config{Daemon: config.DaemonConfig{MultiPathsEnabled: true}}
	result := ResolveDaemonMounts(cfg)
	if result.Enabled {
		t.Fatalf("expected daemon mounts disabled when mount paths are empty")
	}
	if len(result.Warnings) == 0 {
		t.Fatalf("expected warning for empty mount paths")
	}
}

func TestResolveDaemonMountsDedupAndOverlap(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "nested")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatalf("failed to create nested path: %v", err)
	}

	cfg := &config.Config{Daemon: config.DaemonConfig{MultiPathsEnabled: true, MountPaths: []string{root, root, sub}}}
	result := ResolveDaemonMounts(cfg)
	if !result.Enabled {
		t.Fatalf("expected daemon mounts enabled with valid paths")
	}
	if len(result.Paths) != 2 {
		t.Fatalf("expected 2 unique paths, got %d", len(result.Paths))
	}

	var hasDup, hasOverlap bool
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "duplicated") {
			hasDup = true
		}
		if strings.Contains(warning, "overlap") {
			hasOverlap = true
		}
	}
	if !hasDup {
		t.Fatalf("expected duplicate warning, got %v", result.Warnings)
	}
	if !hasOverlap {
		t.Fatalf("expected overlap warning, got %v", result.Warnings)
	}
}

func TestResolveDaemonMountsCapAndWarn(t *testing.T) {
	root := t.TempDir()
	paths := make([]string, 0, 65)
	for i := 0; i < 65; i++ {
		dir := filepath.Join(root, fmt.Sprintf("dir-%02d", i))
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("failed to create path %d: %v", i, err)
		}
		paths = append(paths, dir)
	}

	cfg := &config.Config{Daemon: config.DaemonConfig{MultiPathsEnabled: true, MountPaths: paths}}
	result := ResolveDaemonMounts(cfg)
	if !result.Enabled {
		t.Fatalf("expected daemon mounts enabled with valid paths")
	}
	if len(result.Paths) != 64 {
		t.Fatalf("expected paths capped at 64, got %d", len(result.Paths))
	}

	var has32, has64 bool
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "large lists") {
			has32 = true
		}
		if strings.Contains(warning, "exceeds 64") {
			has64 = true
		}
	}
	if !has32 || !has64 {
		t.Fatalf("expected warnings for >32 and >64 entries, got %v", result.Warnings)
	}
}

func TestMapDaemonWorkdirFromMounts(t *testing.T) {
	mounts := []DaemonMount{
		{HostPath: "/tmp/root-a", ContainerPath: "/workspaces/a"},
		{HostPath: "/tmp/root-b", ContainerPath: "/workspaces/b"},
	}

	workdir, ok := MapDaemonWorkdirFromMounts("/tmp/root-b", mounts)
	if !ok || workdir != "/workspaces/b" {
		t.Fatalf("expected exact root match, got %v %v", workdir, ok)
	}

	workdir, ok = MapDaemonWorkdirFromMounts("/tmp/root-a/nested/path", mounts)
	if !ok || workdir != "/workspaces/a/nested/path" {
		t.Fatalf("expected nested match, got %v %v", workdir, ok)
	}

	workdir, ok = MapDaemonWorkdirFromMounts("/tmp/other", mounts)
	if ok || workdir != "" {
		t.Fatalf("expected no match, got %v %v", workdir, ok)
	}
}

func TestGenerateDockerComposeOverrideMultiPaths(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	configDir := filepath.Join(tmpDir, ".config", "construct-cli")
	containerDir := filepath.Join(configDir, "container")
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		t.Fatalf("failed to create container dir: %v", err)
	}

	root := t.TempDir()
	pathA := filepath.Join(root, "a")
	pathB := filepath.Join(root, "b")
	if err := os.MkdirAll(pathA, 0755); err != nil {
		t.Fatalf("failed to create mount path A: %v", err)
	}
	if err := os.MkdirAll(pathB, 0755); err != nil {
		t.Fatalf("failed to create mount path B: %v", err)
	}

	configToml := "[daemon]\n" +
		"auto_start = true\n" +
		"multi_paths_enabled = true\n" +
		"mount_paths = [\"" + pathA + "\", \"" + pathB + "\"]\n"

	requiredFiles := map[string]string{
		"Dockerfile":            "FROM alpine\n",
		"powershell.exe":        "binary\n",
		"packages.toml":         "[npm]\npackages = []\n",
		"config.toml":           configToml,
		"docker-compose.yml":    "version: '3'\n",
		"entrypoint.sh":         "#!/bin/bash\n",
		"update-all.sh":         "#!/bin/bash\n",
		"network-filter.sh":     "#!/bin/bash\n",
		"clipper":               "binary\n",
		"clipboard-x11-sync.sh": "#!/bin/bash\n",
		"osascript":             "binary\n",
	}

	for file, content := range requiredFiles {
		path := filepath.Join(containerDir, file)
		if file == "config.toml" || file == "packages.toml" {
			path = filepath.Join(configDir, file)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("failed to create dir for %s: %v", file, err)
		}
		perm := os.FileMode(0644)
		if filepath.Ext(path) == ".sh" {
			perm = 0755
		}
		if err := os.WriteFile(path, []byte(content), perm); err != nil {
			t.Fatalf("failed to write %s: %v", file, err)
		}
	}

	projectPath := "/projects/test"
	if err := GenerateDockerComposeOverride(configDir, projectPath, "bridge"); err != nil {
		t.Fatalf("GenerateDockerComposeOverride failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(containerDir, "docker-compose.override.yml"))
	if err != nil {
		t.Fatalf("failed to read override: %v", err)
	}
	contentStr := string(content)

	cfg := &config.Config{Daemon: config.DaemonConfig{MultiPathsEnabled: true, MountPaths: []string{pathA, pathB}}}
	mounts := ResolveDaemonMounts(cfg)
	if mounts.Hash == "" {
		t.Fatalf("expected mounts hash")
	}
	if !strings.Contains(contentStr, DaemonMountsLabelKey+"="+mounts.Hash) {
		t.Fatalf("expected label for mounts hash, got: %s", contentStr)
	}

	for _, mount := range mounts.Mounts {
		needle := mount.HostPath + ":" + mount.ContainerPath
		if !strings.Contains(contentStr, needle) {
			t.Fatalf("expected mount %s in override, got: %s", needle, contentStr)
		}
	}
}
