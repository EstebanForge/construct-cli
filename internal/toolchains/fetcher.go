package toolchains

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/EstebanForge/construct-cli/internal/ui"
)

// FetchOptions configures modular toolchain downloading.
type FetchOptions struct {
	RegistryHost string // default "ghcr.io"
	RepoPrefix   string // default "estebanforge/construct-toolchain-"
	Tag          string // default "latest"
	Force        bool   // re-download even if already cached and up to date
	HTTPClient   *http.Client
}

// IsToolchainCached reports whether the toolchain directory exists and is non-empty.
func IsToolchainCached(id ToolchainID) bool {
	cacheDir := CacheDir()
	if cacheDir == "" {
		return false
	}
	tcDir := filepath.Join(cacheDir, string(id))
	fi, err := os.Stat(tcDir)
	return err == nil && fi.IsDir()
}

// CachedDigest reads the stored .digest marker file from the cached toolchain directory.
func CachedDigest(id ToolchainID) string {
	cacheDir := CacheDir()
	if cacheDir == "" {
		return ""
	}
	p := filepath.Join(cacheDir, string(id), ".digest")
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

type ociIndex struct {
	SchemaVersion int `json:"schemaVersion"`
	Manifests     []struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Platform  struct {
			Architecture string `json:"architecture"`
			OS           string `json:"os"`
		} `json:"platform"`
	} `json:"manifests"`
}

type ociManifest struct {
	SchemaVersion int    `json:"schemaVersion"`
	MediaType     string `json:"mediaType"`
	Config        struct {
		Digest string `json:"digest"`
	} `json:"config"`
	Layers []struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Size      int64  `json:"size"`
	} `json:"layers"`
}

// FetchToolchain pulls and unpacks an OCI toolchain layer into ~/.cache/construct/toolchains/<id>.
func FetchToolchain(ctx context.Context, id ToolchainID, opts FetchOptions) error {
	host := opts.RegistryHost
	if host == "" {
		host = "ghcr.io"
	}
	repoPrefix := opts.RepoPrefix
	if repoPrefix == "" {
		repoPrefix = "estebanforge/construct-toolchain-"
	}
	tag := opts.Tag
	if tag == "" {
		tag = "latest"
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}

	repo := repoPrefix + string(id)

	token, err := acquireRegistryToken(ctx, client, host, repo)
	if err != nil {
		return fmt.Errorf("token acquisition failed for %s: %w", repo, err)
	}

	manifestDigest, manifestBytes, err := fetchPlatformManifest(ctx, client, host, repo, tag, token, runtime.GOARCH)
	if err != nil {
		return fmt.Errorf("fetch manifest failed for %s:%s: %w", repo, tag, err)
	}

	cacheDir := CacheDir()
	if cacheDir == "" {
		return errors.New("cannot resolve host cache directory")
	}
	destDir := filepath.Join(cacheDir, string(id))

	if !opts.Force && IsToolchainCached(id) {
		currentDigest := CachedDigest(id)
		if currentDigest != "" && currentDigest == manifestDigest {
			return nil
		}
	}

	var manifest ociManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}
	if len(manifest.Layers) == 0 {
		return fmt.Errorf("toolchain manifest has no layers")
	}

	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return fmt.Errorf("create toolchain cache root: %w", err)
	}
	stageDir, err := os.MkdirTemp(cacheDir, fmt.Sprintf("stage-%s-*", id))
	if err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}
	defer os.RemoveAll(stageDir) //nolint:errcheck

	prefixToStrip := fmt.Sprintf("opt/toolchains/%s", id)

	for _, layer := range manifest.Layers {
		if err := downloadAndExtractLayer(ctx, client, host, repo, layer.Digest, token, stageDir, prefixToStrip); err != nil {
			return fmt.Errorf("extract layer %s: %w", layer.Digest, err)
		}
	}

	_ = os.WriteFile(filepath.Join(stageDir, ".digest"), []byte(manifestDigest+"\n"), 0644) //nolint:errcheck // cache metadata

	backupDir := filepath.Join(cacheDir, fmt.Sprintf(".old-%s-%d", id, time.Now().UnixNano()))
	if fi, err := os.Stat(destDir); err == nil && fi.IsDir() {
		if err := os.Rename(destDir, backupDir); err != nil {
			return fmt.Errorf("backup existing toolchain %s: %w", id, err)
		}
		defer os.RemoveAll(backupDir) //nolint:errcheck
	}

	if err := os.Rename(stageDir, destDir); err != nil {
		if _, statErr := os.Stat(backupDir); statErr == nil {
			_ = os.Rename(backupDir, destDir) //nolint:errcheck // best-effort restore
		}
		return fmt.Errorf("activate toolchain %s: %w", id, err)
	}

	return nil
}

func acquireRegistryToken(ctx context.Context, client *http.Client, host, repo string) (string, error) {
	if host != "ghcr.io" {
		return "", nil
	}

	url := fmt.Sprintf("https://%s/token?scope=repository:%s:pull", host, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024)) //nolint:errcheck // best-effort error body
		return "", fmt.Errorf("token request returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var res struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}
	return res.Token, nil
}

func fetchPlatformManifest(ctx context.Context, client *http.Client, host, repo, tagOrDigest, token, targetArch string) (string, []byte, error) {
	scheme := "https"
	if strings.HasPrefix(host, "127.0.0.1") || strings.HasPrefix(host, "localhost") {
		scheme = "http"
	}

	url := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", scheme, host, repo, tagOrDigest)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", strings.Join([]string{
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
	}, ", "))

	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048)) //nolint:errcheck // best-effort error body
		return "", nil, fmt.Errorf("manifest request returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", nil, err
	}

	digest := resp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		h := sha256.Sum256(body)
		digest = "sha256:" + hex.EncodeToString(h[:])
	}

	var index ociIndex
	if err := json.Unmarshal(body, &index); err == nil && len(index.Manifests) > 0 {
		for _, m := range index.Manifests {
			if m.Platform.OS == "linux" && m.Platform.Architecture == targetArch {
				return fetchPlatformManifest(ctx, client, host, repo, m.Digest, token, targetArch)
			}
		}
		// Fallback to first manifest if exact arch match not tagged
		return fetchPlatformManifest(ctx, client, host, repo, index.Manifests[0].Digest, token, targetArch)
	}

	return digest, body, nil
}

func downloadAndExtractLayer(ctx context.Context, client *http.Client, host, repo, digest, token, stageDir, prefixToStrip string) error {
	scheme := "https"
	if strings.HasPrefix(host, "127.0.0.1") || strings.HasPrefix(host, "localhost") {
		scheme = "http"
	}

	url := fmt.Sprintf("%s://%s/v2/%s/blobs/%s", scheme, host, repo, digest)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download blob returned %d", resp.StatusCode)
	}

	var r io.Reader = resp.Body

	gz, err := gzip.NewReader(resp.Body)
	if err == nil {
		defer gz.Close() //nolint:errcheck
		r = gz
	}

	tr := tar.NewReader(r)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}

		cleanPath := filepath.Clean(header.Name)
		cleanPath = strings.TrimPrefix(cleanPath, "/")

		if strings.HasPrefix(cleanPath, prefixToStrip) {
			cleanPath = strings.TrimPrefix(cleanPath, prefixToStrip)
			cleanPath = strings.TrimPrefix(cleanPath, "/")
		}

		if cleanPath == "" || cleanPath == "." {
			continue
		}

		destPath := filepath.Join(stageDir, cleanPath)

		rel, err := filepath.Rel(stageDir, destPath)
		if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
			continue // Tar slip protection
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(destPath, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(destPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, header.FileInfo().Mode())
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close() //nolint:errcheck
				return err
			}
			f.Close() //nolint:errcheck
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				return err
			}
			_ = os.Remove(destPath) //nolint:errcheck // best-effort cleanup before symlink
			if err := os.Symlink(header.Linkname, destPath); err != nil {
				return err
			}
		}
	}

	return nil
}

// EnsureToolchains checks detected toolchains and fetches missing ones.
func EnsureToolchains(ctx context.Context, ids []ToolchainID) error {
	for _, id := range ids {
		if !IsToolchainCached(id) {
			ui.InfoF("📦 Downloading toolchain: %s...\n", id)
			if err := FetchToolchain(ctx, id, FetchOptions{}); err != nil {
				ui.InfoF("Warning: could not fetch toolchain %s: %v (falling back to image tools)\n", id, err)
			} else {
				ui.InfoF("✓ Toolchain %s ready\n", id)
			}
		}
	}
	return nil
}
