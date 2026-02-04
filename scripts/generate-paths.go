//go:build ignore

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/env"
)

const (
	startMarker = "Construct: PATH_COMPONENTS_START"
	endMarker   = "Construct: PATH_COMPONENTS_END"
	homeDir     = "/home/construct"
)

type renderTarget struct {
	path    string
	content func() (string, error)
}

func main() {
	targets := []renderTarget{
		{
			path: "internal/templates/entrypoint.sh",
			content: func() (string, error) {
				lines := make([]string, 0, len(env.PathComponents))
				for _, component := range env.PathComponents {
					lines = append(lines, fmt.Sprintf("add_path %q", component))
				}
				return strings.Join(lines, "\n") + "\n", nil
			},
		},
		{
			path: "internal/templates/docker-compose.yml",
			content: func() (string, error) {
				pathValue := buildPathString(homeDir)
				return "      - PATH=" + pathValue + "\n", nil
			},
		},
		{
			path: "internal/templates/Dockerfile",
			content: func() (string, error) {
				pathValue := buildPathString(homeDir)
				return "ENV PATH=\"" + pathValue + "\"\n", nil
			},
		},
	}

	root, err := repoRoot()
	if err != nil {
		fail(err)
	}

	var updated []string
	for _, target := range targets {
		fullPath := filepath.Join(root, target.path)
		contents, err := os.ReadFile(fullPath)
		if err != nil {
			fail(fmt.Errorf("read %s: %w", target.path, err))
		}

		block, err := target.content()
		if err != nil {
			fail(fmt.Errorf("render %s: %w", target.path, err))
		}

		next, err := replaceBetweenMarkers(string(contents), block)
		if err != nil {
			fail(fmt.Errorf("update %s: %w", target.path, err))
		}

		if next == string(contents) {
			continue
		}

		if err := os.WriteFile(fullPath, []byte(next), 0644); err != nil {
			fail(fmt.Errorf("write %s: %w", target.path, err))
		}
		updated = append(updated, target.path)
	}

	if len(updated) > 0 {
		fmt.Printf("Updated PATH blocks: %s\n", strings.Join(updated, ", "))
	}
}

func buildPathString(home string) string {
	paths := make([]string, 0, len(env.PathComponents))
	for _, component := range env.PathComponents {
		paths = append(paths, strings.ReplaceAll(component, "$HOME", home))
	}
	return strings.Join(paths, ":")
}

func replaceBetweenMarkers(content, replacement string) (string, error) {
	start := strings.Index(content, startMarker)
	if start == -1 {
		return "", fmt.Errorf("start marker not found")
	}
	end := strings.Index(content, endMarker)
	if end == -1 {
		return "", fmt.Errorf("end marker not found")
	}
	if end < start {
		return "", fmt.Errorf("end marker before start marker")
	}

	startLineEnd := strings.Index(content[start:], "\n")
	if startLineEnd == -1 {
		return "", fmt.Errorf("start marker line missing newline")
	}
	startLineEnd = start + startLineEnd + 1

	endLineStart := strings.LastIndex(content[:end], "\n")
	if endLineStart == -1 {
		endLineStart = 0
	} else {
		endLineStart++
	}

	return content[:startLineEnd] + replacement + content[endLineStart:], nil
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("repo root not found")
		}
		dir = parent
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
