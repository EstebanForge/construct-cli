package safefs

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestReadFileRegular(t *testing.T) {
	dir := t.TempDir()
	reg := filepath.Join(dir, "regular.txt")
	if err := os.WriteFile(reg, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	data, err := ReadFileRegular(reg, 1<<20)
	if err != nil || string(data) != "payload" {
		t.Fatalf("regular read: data=%q err=%v", data, err)
	}

	link := filepath.Join(dir, "link.txt")
	secret := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secret, []byte("host-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFileRegular(link, 1<<20); err == nil {
		t.Fatal("symlink source must be refused, not followed")
	}

	dangling := filepath.Join(dir, "dangling.txt")
	if err := os.Symlink(filepath.Join(dir, "nope"), dangling); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFileRegular(dangling, 1<<20); err == nil {
		t.Fatal("dangling symlink source must be refused")
	}

	fifoAsFile := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(fifoAsFile, make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFileRegular(fifoAsFile, 1024); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversize file must be refused with a size error, got %v", err)
	}
}

// TestRefusesPlantedComponents: the bind paths run through guest-writable
// directories, so a symlinked INTERMEDIATE component (not just the leaf)
// must refuse — a planted dir symlink persists and would redirect every
// later host open, no race required.
func TestRefusesPlantedComponents(t *testing.T) {
	root := t.TempDir()
	dirReal := filepath.Join(root, "real")
	if err := os.MkdirAll(dirReal, 0o755); err != nil {
		t.Fatal(err)
	}
	reg := filepath.Join(dirReal, "file.txt")
	if err := os.WriteFile(reg, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFileRegular(reg, 1<<20); err != nil {
		t.Fatalf("baseline read: %v", err)
	}
	if err := WriteFile(reg, []byte("payload2"), 0o644); err != nil {
		t.Fatalf("baseline write: %v", err)
	}

	// Plant a symlinked directory pointing at a WRITABLE outside target:
	// the refusal must be the guard, not /etc's permissions. A write that
	// lands in outsideDir proves the escape.
	if err := os.Rename(dirReal, filepath.Join(root, "real.bak")); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, dirReal); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFileRegular(reg, 1<<20); err == nil {
		t.Error("a symlinked intermediate directory must refuse reads")
	}
	if err := WriteFile(reg, []byte("escaped"), 0o644); err == nil {
		t.Error("a symlinked intermediate directory must refuse writes")
	}
	if _, err := os.Stat(filepath.Join(outside, "file.txt")); !os.IsNotExist(err) {
		t.Error("the write must not land in the symlink target")
	}
}

// TestReadFileRegularRefusesFifoSwap pins the FIFO defense: a guest can
// swap a regular file for a FIFO after any check; the read must fail
// instead of hanging the host on the pipe open.
func TestReadFileRegularRefusesFifoSwap(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	if _, err := ReadFileRegular(fifo, 1<<20); err == nil {
		t.Error("a FIFO must be refused, not opened")
	}
}

// TestRefusesMultiHopSymlinkTraversal pins the component walk against a
// symlink CHAIN: dir -> symlink -> symlink -> outside target. One-hop
// refusals are covered by TestRefusesPlantedComponents; a chain must not
// pass because the final hop resolves outside the tree.
func TestRefusesMultiHopSymlinkTraversal(t *testing.T) {
	root := t.TempDir()
	aDir := filepath.Join(root, "a")
	bDir := filepath.Join(aDir, "b")
	cDir := filepath.Join(bDir, "c")
	if err := os.MkdirAll(cDir, 0o755); err != nil {
		t.Fatal(err)
	}
	reg := filepath.Join(cDir, "file.txt")
	if err := os.WriteFile(reg, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	outside := t.TempDir()
	hop1 := filepath.Join(root, "hop1")
	hop2 := filepath.Join(root, "hop2")
	if err := os.Symlink(outside, hop1); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(hop1, hop2); err != nil {
		t.Fatal(err)
	}
	// Rebuild a/b as a symlink chain escaping through hop2.
	if err := os.Rename(bDir, filepath.Join(root, "b.bak")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(hop2, bDir); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadFileRegular(reg, 1<<20); err == nil {
		t.Error("a symlinked component chain must refuse reads")
	}
	if err := WriteFile(reg, []byte("escaped"), 0o644); err == nil {
		t.Error("a symlinked component chain must refuse writes")
	}
	if _, err := os.Stat(filepath.Join(outside, "file.txt")); !os.IsNotExist(err) {
		t.Error("the write must not land in the chain target")
	}
}

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "out.txt")
	if err := WriteFile(dst, []byte("v1"), 0o644); err != nil {
		t.Fatalf("fresh write: %v", err)
	}
	if data, err := os.ReadFile(dst); err != nil || string(data) != "v1" {
		t.Fatalf("fresh write content: %q err=%v", data, err)
	}

	if err := WriteFile(dst, []byte("v2"), 0o644); err != nil {
		t.Fatalf("overwrite of regular file: %v", err)
	}

	link := filepath.Join(dir, "victim.txt")
	target := filepath.Join(dir, "target.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(link, []byte("evil"), 0o644); err == nil {
		t.Fatal("write through a symlinked destination must be refused")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("symlink target must not have been created")
	}

	dangling := filepath.Join(dir, "dangle-out")
	if err := os.Symlink(filepath.Join(dir, "elsewhere"), dangling); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(dangling, []byte("evil"), 0o644); err == nil {
		t.Fatal("write to a dangling symlink destination must be refused")
	}
}
