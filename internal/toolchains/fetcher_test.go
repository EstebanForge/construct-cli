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
		Linkname string
		Mode     int64
		Typeflag byte
	}{
		{Name: "opt/toolchains/testtc/bin/mycmd", Body: "#!/bin/sh\necho hello\n", Mode: 0755, Typeflag: tar.TypeReg},
		{Name: "opt/toolchains/testtc/lib/testlib.txt", Body: "library contents\n", Mode: 0644, Typeflag: tar.TypeReg},
		{Name: "opt/toolchains/testtc/escape/../../bad.txt", Body: "evil\n", Mode: 0644, Typeflag: tar.TypeReg},
		{Name: "opt/toolchains/testtc/bin/symcmd", Linkname: "mycmd", Mode: 0755, Typeflag: tar.TypeSymlink},
		{Name: "opt/toolchains/testtc/bin/badlink", Linkname: "../../../../../etc/passwd", Mode: 0755, Typeflag: tar.TypeSymlink},
	}

	for _, file := range files {
		hdr := &tar.Header{
			Name:     file.Name,
			Linkname: file.Linkname,
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

	badLinkPath := filepath.Join(tmpCache, "construct", "toolchains", "testtc", "bin", "badlink")
	if _, err := os.Lstat(badLinkPath); err == nil {
		t.Errorf("escaping symlink was extracted: %s exists", badLinkPath)
	}

	symCmdPath := filepath.Join(tmpCache, "construct", "toolchains", "testtc", "bin", "symcmd")
	if _, err := os.Lstat(symCmdPath); err != nil {
		t.Errorf("valid intra-toolchain symlink missing: %v", err)
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

func TestFetchToolchainDigestMismatch(t *testing.T) {
	tmpCache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmpCache)

	layerBytes, _ := createTestLayerTarGz(t)
	corruptDigest := "sha256:0000000000000000000000000000000000000000000000000000000000000000"

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
		Digest:    corruptDigest,
		Size:      int64(len(layerBytes)),
	})

	manifestBytes, err := json.Marshal(manifestObj)
	if err != nil {
		t.Fatal(err)
	}

	indexObj := ociIndex{SchemaVersion: 2}
	indexObj.Manifests = append(indexObj.Manifests, struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Platform  struct {
			Architecture string `json:"architecture"`
			OS           string `json:"os"`
		} `json:"platform"`
	}{
		MediaType: "application/vnd.oci.image.manifest.v1+json",
		Digest:    fmt.Sprintf("sha256:%x", sha256.Sum256(manifestBytes)),
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

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasSuffix(p, "/manifests/latest") {
			w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
			_, _ = w.Write(indexBytes)
			return
		}
		if strings.Contains(p, "/manifests/sha256:") {
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			_, _ = w.Write(manifestBytes)
			return
		}
		if strings.Contains(p, "/blobs/") {
			w.Header().Set("Content-Type", "application/vnd.oci.image.layer.v1.tar+gzip")
			_, _ = w.Write(layerBytes)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	opts := FetchOptions{
		RegistryHost: u.Host,
		RepoPrefix:   "test/",
		Tag:          "latest",
		HTTPClient:   server.Client(),
	}

	err = FetchToolchain(context.Background(), ToolchainID("corrupt"), opts)
	if err == nil {
		t.Fatal("expected error on layer digest mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("expected digest mismatch error, got: %v", err)
	}
}

func TestEnsureNoSymlinkAncestors(t *testing.T) {
	tmpDir := t.TempDir()
	stageDir := filepath.Join(tmpDir, "stage")
	if err := os.MkdirAll(stageDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Legitimate path
	normalPath := filepath.Join(stageDir, "bin", "tool")
	if err := ensureNoSymlinkAncestors(stageDir, normalPath); err != nil {
		t.Fatalf("expected clean path to pass, got: %v", err)
	}

	// Create a directory symlink: stage/symdir -> stage
	symDir := filepath.Join(stageDir, "symdir")
	if err := os.Symlink(stageDir, symDir); err != nil {
		t.Fatal(err)
	}

	// Attempt to traverse through symdir
	evasionPath := filepath.Join(symDir, "evil")
	if err := ensureNoSymlinkAncestors(stageDir, evasionPath); err == nil {
		t.Fatal("expected symlink directory ancestor traversal to be rejected, got nil")
	}
}

func TestSymlinkChainEscapeRejected(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range []tar.Header{
		{Name: "top/", Typeflag: tar.TypeDir, Mode: 0755},
		{Name: "top/x", Typeflag: tar.TypeSymlink, Linkname: "../"},
		{Name: "top/y", Typeflag: tar.TypeSymlink, Linkname: "x/../.."},
	} {
		h := e
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	_ = gz.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()

	stage := t.TempDir()
	total := int64(0)
	_ = downloadAndExtractLayer(context.Background(), srv.Client(), srv.Listener.Addr().String(), "r/x", "", "", stage, "opt/toolchains/x", &total)

	// top/y should not exist, or must not escape stageDir
	yPath := filepath.Join(stage, "top", "y")
	if _, err := os.Lstat(yPath); err == nil {
		evalPath, rerr := filepath.EvalSymlinks(yPath)
		if rerr == nil {
			rel, relErr := filepath.Rel(stage, evalPath)
			if relErr != nil || strings.HasPrefix(rel, "..") {
				t.Fatalf("top/y escaped stageDir: real=%q, stage=%q", evalPath, stage)
			}
		}
	}
}

func TestCumulativeExtractionCap(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	content := bytes.Repeat([]byte("A"), 1024)
	h := tar.Header{
		Name:     "tool.txt",
		Mode:     0644,
		Size:     int64(len(content)),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(&h); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()

	stage := t.TempDir()
	// Pre-seed totalExtracted right at the boundary so this 1024-byte layer pushes it over
	total := int64(maxExtractedBytes - 500)
	err := downloadAndExtractLayer(context.Background(), srv.Client(), srv.Listener.Addr().String(), "r/x", "", "", stage, "", &total)
	if err == nil {
		t.Fatal("expected cumulative extraction cap error, got nil")
	}
	if !strings.Contains(err.Error(), "exceeded max extraction limit") {
		t.Fatalf("expected size cap error message, got: %v", err)
	}
}
