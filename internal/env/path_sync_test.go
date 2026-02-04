package env

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathComponentsSync(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}

	entrypointPath := filepath.Join(root, "internal", "templates", "entrypoint.sh")
	composePath := filepath.Join(root, "internal", "templates", "docker-compose.yml")
	dockerfilePath := filepath.Join(root, "internal", "templates", "Dockerfile")

	entrypointPaths, err := readEntrypointPaths(entrypointPath)
	if err != nil {
		t.Fatalf("entrypoint paths: %v", err)
	}

	if !equalStringSlices(entrypointPaths, PathComponents) {
		t.Fatalf("entrypoint.sh add_path list does not match PathComponents")
	}

	expected := BuildConstructPath("/home/construct")

	composePathValue, err := readComposePath(composePath)
	if err != nil {
		t.Fatalf("docker-compose PATH: %v", err)
	}
	if composePathValue != expected {
		t.Fatalf("docker-compose PATH mismatch")
	}

	dockerfilePathValue, err := readDockerfilePath(dockerfilePath)
	if err != nil {
		t.Fatalf("Dockerfile PATH: %v", err)
	}
	if dockerfilePathValue != expected {
		t.Fatalf("Dockerfile PATH mismatch")
	}
}

func readEntrypointPaths(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var paths []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "add_path ") {
			continue
		}
		start := strings.Index(line, "\"")
		end := strings.LastIndex(line, "\"")
		if start == -1 || end == -1 || end <= start {
			return nil, errInvalidLine(line)
		}
		paths = append(paths, line[start+1:end])
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return paths, nil
}

func readComposePath(path string) (string, error) {
	return readSinglePathValue(path, "- PATH=")
}

func readDockerfilePath(path string) (string, error) {
	return readSinglePathValue(path, "ENV PATH=\"")
}

func readSinglePathValue(path, prefix string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, prefix) {
			continue
		}

		value := strings.TrimPrefix(line, prefix)
		if strings.HasSuffix(prefix, "\"") {
			value = strings.TrimSuffix(value, "\"")
		}
		return value, nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", errPathNotFound(prefix)
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
			return "", errPathNotFound("go.mod")
		}
		dir = parent
	}
}

type errString struct {
	msg string
}

func (e errString) Error() string {
	return e.msg
}

func errInvalidLine(line string) error {
	return errString{msg: "invalid add_path line: " + line}
}

func errPathNotFound(target string) error {
	return errString{msg: "path value not found: " + target}
}
