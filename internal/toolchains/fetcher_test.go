package toolchains

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func createTestLayerTarGz(t *testing.T) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	files := []struct {
		Name     string
		Body     string
		Mode     int64
		Typeflag byte
	}{
		{Name: "opt/toolchains/testtc/bin/mycmd", Body: "#!/bin/sh\necho hello\n", Mode: 0755, Typeflag: tar.TypeReg},
		{Name: "opt/toolchains/testtc/lib/testlib.txt", Body: "library contents\n", Mode: 0644, Typeflag: tar.TypeReg},
		{Name: "opt/toolchains/testtc/escape/../../bad.txt", Body: "evil\n", Mode: 0644, Typeflag: tar.TypeReg},
	}

	for _, file := range files {
		hdr := &tar.Header{
			Name:     file.Name,
			Mode:     file.Mode,
			Size:     int64(len(file.Body)),
			Typeflag: file.Typeflag,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(file.Body)); err != nil {
			t.Fatal(err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}

	data := buf.Bytes()
	h := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(h[:])
	return data, digest
}

func TestFetchToolchain(t *testing.T) {
	tmpCache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmpCache)

	layerBytes, layerDigest := createTestLayerTarGz(t)

	manifestObj := ociManifest{
		SchemaVersion: 2,
		MediaType:     "application/vnd.oci.image.manifest.v1+json",
	}
	manifestObj.Layers = append(manifestObj.Layers, struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Size      int64  `json:"size"`
	}{
		MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
		Digest:    layerDigest,
		Size:      int64(len(layerBytes)),
	})

	manifestBytes, err := json.Marshal(manifestObj)
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(manifestBytes))

	indexObj := ociIndex{
		SchemaVersion: 2,
	}
	indexObj.Manifests = append(indexObj.Manifests, struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Platform  struct {
			Architecture string `json:"architecture"`
			OS           string `json:"os"`
		} `json:"platform"`
	}{
		MediaType: "application/vnd.oci.image.manifest.v1+json",
		Digest:    manifestDigest,
		Platform: struct {
			Architecture string `json:"architecture"`
			OS           string `json:"os"`
		}{
			Architecture: runtime.GOARCH,
			OS:           "linux",
		},
	})
	indexBytes, err := json.Marshal(indexObj)
	if err != nil {
		t.Fatal(err)
	}

	blobHitCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/manifests/latest"):
			w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
			_, _ = w.Write(indexBytes)
		case strings.HasSuffix(path, "/manifests/"+manifestDigest):
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			w.Header().Set("Docker-Content-Digest", manifestDigest)
			_, _ = w.Write(manifestBytes)
		case strings.HasSuffix(path, "/blobs/"+layerDigest):
			blobHitCount++
			w.Header().Set("Content-Type", "application/vnd.oci.image.layer.v1.tar+gzip")
			_, _ = w.Write(layerBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	opts := FetchOptions{
		RegistryHost: u.Host,
		RepoPrefix:   "test/",
		Tag:          "latest",
		HTTPClient:   server.Client(),
	}

	testID := ToolchainID("testtc")

	if IsToolchainCached(testID) {
		t.Fatalf("expected toolchain not cached initially")
	}

	ctx := context.Background()
	if err := FetchToolchain(ctx, testID, opts); err != nil {
		t.Fatalf("FetchToolchain failed: %v", err)
	}

	if !IsToolchainCached(testID) {
		t.Fatalf("expected toolchain to be cached after fetch")
	}

	if gotDigest := CachedDigest(testID); gotDigest != manifestDigest {
		t.Errorf("expected cached digest %q, got %q", manifestDigest, gotDigest)
	}

	binPath := filepath.Join(tmpCache, "construct", "toolchains", "testtc", "bin", "mycmd")
	cmdData, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("failed to read extracted binary: %v", err)
	}
	if !strings.Contains(string(cmdData), "echo hello") {
		t.Errorf("unexpected binary content: %s", string(cmdData))
	}

	fi, err := os.Stat(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&0111 == 0 {
		t.Errorf("expected binary to be executable, got mode: %v", fi.Mode())
	}

	badPath := filepath.Join(tmpCache, "construct", "bad.txt")
	if _, err := os.Stat(badPath); err == nil {
		t.Errorf("tar slip file escaped sandbox: %s exists", badPath)
	}

	// Re-fetch with Force=false should skip downloading layer blob
	if err := FetchToolchain(ctx, testID, opts); err != nil {
		t.Fatalf("second FetchToolchain failed: %v", err)
	}
	if blobHitCount != 1 {
		t.Errorf("expected blob hit count 1 (cache hit), got %d", blobHitCount)
	}

	// Re-fetch with Force=true should re-download layer blob
	opts.Force = true
	if err := FetchToolchain(ctx, testID, opts); err != nil {
		t.Fatalf("forced FetchToolchain failed: %v", err)
	}
	if blobHitCount != 2 {
		t.Errorf("expected blob hit count 2 after forced fetch, got %d", blobHitCount)
	}
}

func TestEnsureToolchains(t *testing.T) {
	tmpCache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmpCache)

	// Pre-seed cached toolchain
	goDir := filepath.Join(tmpCache, "construct", "toolchains", "go")
	if err := os.MkdirAll(goDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Calling EnsureToolchains for cached + uncached should not crash and return nil
	ctx := context.Background()
	err := EnsureToolchains(ctx, []ToolchainID{ToolchainGo, ToolchainRust})
	if err != nil {
		t.Errorf("EnsureToolchains should fail-open with warning, got err: %v", err)
	}
}
