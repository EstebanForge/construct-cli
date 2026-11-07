package toolchains

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectToolchains(t *testing.T) {
	tmpDir := t.TempDir()

	// Empty dir should detect nothing
	if len(DetectToolchains(tmpDir)) != 0 {
		t.Fatalf("expected 0 toolchains for empty dir")
	}

	// Create Node + Go project markers
	if err := os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	detected := DetectToolchains(tmpDir)
	if len(detected) != 2 {
		t.Fatalf("expected 2 toolchains, got %d", len(detected))
	}
	if detected[0] != ToolchainNode || detected[1] != ToolchainGo {
		t.Errorf("unexpected toolchains order: %v", detected)
	}
}

func TestResolveMounts(t *testing.T) {
	cacheDir := "/home/test/.cache/construct/toolchains"
	mounts := ResolveMounts(cacheDir, []ToolchainID{ToolchainRust, ToolchainPython})

	if len(mounts) != 2 {
		t.Fatalf("expected 2 mounts, got %d", len(mounts))
	}
	if mounts[0].HostPath != "/home/test/.cache/construct/toolchains/rust" || mounts[0].GuestPath != "/opt/toolchains/rust" {
		t.Errorf("unexpected rust mount: %+v", mounts[0])
	}
	if mounts[1].HostPath != "/home/test/.cache/construct/toolchains/python" || mounts[1].GuestPath != "/opt/toolchains/python" {
		t.Errorf("unexpected python mount: %+v", mounts[1])
	}
}

func TestResolveExistingMountsAndAvailable(t *testing.T) {
	cacheDir := t.TempDir()

	// Create only rust dir
	rustDir := filepath.Join(cacheDir, "rust")
	if err := os.Mkdir(rustDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Resolve existing for rust and python
	existing := ResolveExistingMounts(cacheDir, []ToolchainID{ToolchainRust, ToolchainPython})
	if len(existing) != 1 {
		t.Fatalf("expected 1 existing mount, got %d", len(existing))
	}
	if existing[0].ID != ToolchainRust {
		t.Errorf("expected rust mount, got %s", existing[0].ID)
	}

	// Available toolchains in cache
	avail := AvailableToolchains(cacheDir)
	if len(avail) != 1 {
		t.Fatalf("expected 1 available toolchain, got %d", len(avail))
	}
	if avail[0].ID != ToolchainRust {
		t.Errorf("expected rust toolchain, got %s", avail[0].ID)
	}
}

func TestDistributionArtifact(t *testing.T) {
	ref := DistributionArtifact(ToolchainNode)
	expected := "ghcr.io/estebanforge/construct-toolchain-node:latest"
	if ref != expected {
		t.Errorf("expected %q, got %q", expected, ref)
	}
}

func TestBuildToolchainPath(t *testing.T) {
	basePath := "/usr/bin:/bin"
	path := BuildToolchainPath(basePath, []ToolchainID{ToolchainNode, ToolchainRust})

	expected := "/opt/toolchains/node/bin:/opt/toolchains/rust/bin:/usr/bin:/bin"
	if path != expected {
		t.Errorf("expected PATH %q, got %q", expected, path)
	}

	if BuildToolchainPath(basePath, nil) != basePath {
		t.Errorf("expected unaltered basePath for empty toolchains")
	}
}
