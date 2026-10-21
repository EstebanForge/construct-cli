package safefs

import (
	"os"
	"path/filepath"
	"strings"
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
