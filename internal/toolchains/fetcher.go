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

// Maximum allowable extracted bytes across layers / toolchain downloads (4 GB).
const (
	maxLayerBytes     = 2 << 30
	maxExtractedBytes = 4 << 30
	maxFileBytes      = 1 << 30
)

// FetchOptions configures modular toolchain downloading.
type FetchOptions struct {
	RegistryHost   string // default "ghcr.io"
	RepoPrefix     string // default "estebanforge/construct-toolchain-"
	Tag            string // default "latest"
	Force          bool   // re-download even if already cached and up to date
	HTTPClient     *http.Client
	TotalExtracted *int64 // cumulative extracted byte counter across layers and toolchains
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

	totalExtracted := opts.TotalExtracted
	if totalExtracted == nil {
		var localTotal int64
		totalExtracted = &localTotal
	}
	for _, layer := range manifest.Layers {
		if err := downloadAndExtractLayer(ctx, client, host, repo, layer.Digest, token, stageDir, prefixToStrip, totalExtracted); err != nil {
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

// ensureNoSymlinkAncestors verifies that all existing directory components
// between stageDir and destPath are genuine directories, not symlinks.
func ensureNoSymlinkAncestors(stageDir, destPath string) error {
	rel, err := filepath.Rel(stageDir, destPath)
	if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
		return fmt.Errorf("invalid path outside stage: %s", destPath)
	}
	parts := strings.Split(filepath.Dir(rel), string(filepath.Separator))
	curr := stageDir
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		curr = filepath.Join(curr, part)
		fi, err := os.Lstat(curr)
		if err != nil {
			if os.IsNotExist(err) {
				break
			}
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path component is a symlink: %s", curr)
		}
	}
	return nil
}

func downloadAndExtractLayer(ctx context.Context, client *http.Client, host, repo, digest, token, stageDir, prefixToStrip string, totalExtracted *int64) error {
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

	// Bound layer download size and compute sha256 of raw layer blob.
	if totalExtracted == nil {
		var zero int64
		totalExtracted = &zero
	}
	bodyReader := io.LimitReader(resp.Body, maxLayerBytes)
	hasher := sha256.New()
	layerStream := io.TeeReader(bodyReader, hasher)

	r := layerStream
	var gz *gzip.Reader

	gzReader, err := gzip.NewReader(layerStream)
	if err == nil {
		gz = gzReader
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
			if err := ensureNoSymlinkAncestors(stageDir, destPath); err != nil {
				return err
			}
			if fi, err := os.Lstat(destPath); err == nil && fi.Mode()&os.ModeSymlink != 0 {
				_ = os.Remove(destPath) //nolint:errcheck
			}
			if err := os.MkdirAll(destPath, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := ensureNoSymlinkAncestors(stageDir, destPath); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				return err
			}
			// Unlink existing node to prevent following planted symlinks.
			if _, statErr := os.Lstat(destPath); statErr == nil {
				_ = os.Remove(destPath) //nolint:errcheck
			}
			f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, header.FileInfo().Mode())
			if err != nil {
				return err
			}
			lr := io.LimitReader(tr, maxFileBytes+1)
			n, err := io.Copy(f, lr)
			f.Close() //nolint:errcheck
			if err != nil {
				return err
			}
			if n > maxFileBytes {
				return fmt.Errorf("toolchain file %s exceeded max file size (%d bytes)", cleanPath, maxFileBytes)
			}
			*totalExtracted += n
			if *totalExtracted > maxExtractedBytes {
				return fmt.Errorf("toolchain archive exceeded max extraction limit (%d bytes)", maxExtractedBytes)
			}
		case tar.TypeSymlink:
			if err := ensureNoSymlinkAncestors(stageDir, destPath); err != nil {
				return err
			}
			linkTarget := header.Linkname
			if filepath.IsAbs(linkTarget) {
				continue // Refuse absolute symlinks
			}
			resolvedTarget := filepath.Clean(filepath.Join(filepath.Dir(destPath), linkTarget))
			relTarget, err := filepath.Rel(stageDir, resolvedTarget)
			if err != nil || strings.HasPrefix(relTarget, "..") {
				continue // Refuse escaping symlinks
			}
			if !resolvesWithinRoot(stageDir, filepath.Dir(destPath), linkTarget) {
				continue // Refuse symlink-chain escaping targets
			}
			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				return err
			}
			_ = os.Remove(destPath) //nolint:errcheck // best-effort cleanup before symlink
			if err := os.Symlink(linkTarget, destPath); err != nil {
				return err
			}
			if evalTarget, err := filepath.EvalSymlinks(destPath); err == nil {
				evalStage := evalPathPreservingTrailing(stageDir)
				if rel, err := filepath.Rel(evalStage, evalTarget); err != nil || isPathEscaping(rel) {
					_ = os.Remove(destPath) //nolint:errcheck
					continue
				}
			}
		case tar.TypeLink:
			if err := ensureNoSymlinkAncestors(stageDir, destPath); err != nil {
				return err
			}
			linkTarget := header.Linkname
			cleanTarget := filepath.Clean(linkTarget)
			cleanTarget = strings.TrimPrefix(cleanTarget, "/")
			if strings.HasPrefix(cleanTarget, prefixToStrip) {
				cleanTarget = strings.TrimPrefix(cleanTarget, prefixToStrip)
				cleanTarget = strings.TrimPrefix(cleanTarget, "/")
			}
			oldPath := filepath.Join(stageDir, cleanTarget)
			relOld, err := filepath.Rel(stageDir, oldPath)
			if err != nil || strings.HasPrefix(relOld, "..") {
				continue
			}
			if err := ensureNoSymlinkAncestors(stageDir, oldPath); err != nil {
				return err
			}
			oldInfo, err := os.Lstat(oldPath)
			if err != nil || (oldInfo.Mode()&os.ModeSymlink != 0) {
				continue // Refuse hardlinking symlinks
			}
			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				return err
			}
			_ = os.Remove(destPath) //nolint:errcheck
			if err := os.Link(oldPath, destPath); err != nil {
				return err
			}
		}
	}

	// Drain remaining compressed bytes to finish layer sha256 calculation.
	if gz != nil {
		if _, err := io.Copy(io.Discard, gz); err != nil {
			return fmt.Errorf("failed to drain compressed layer: %w", err)
		}
	}
	if _, err := io.Copy(io.Discard, layerStream); err != nil {
		return fmt.Errorf("failed to drain layer stream: %w", err)
	}

	actualDigest := "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	if digest != "" && !strings.EqualFold(actualDigest, digest) {
		return fmt.Errorf("toolchain layer digest mismatch: expected %s, got %s", digest, actualDigest)
	}

	return nil
}

// EnsureToolchains checks detected toolchains and fetches missing ones,
// enforcing a cumulative extraction cap across all downloaded toolchains.
func EnsureToolchains(ctx context.Context, ids []ToolchainID) error {
	var cumulativeExtracted int64
	for _, id := range ids {
		if !IsToolchainCached(id) {
			ui.InfoF("📦 Downloading toolchain: %s...\n", id)
			if err := FetchToolchain(ctx, id, FetchOptions{TotalExtracted: &cumulativeExtracted}); err != nil {
				ui.InfoF("Warning: could not fetch toolchain %s: %v (falling back to image tools)\n", id, err)
			} else {
				ui.InfoF("✓ Toolchain %s ready\n", id)
			}
		}
	}
	return nil
}

// isPathEscaping reports whether rel escapes its parent root.
func isPathEscaping(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// evalPathPreservingTrailing resolves symlinks on the nearest existing ancestor of path
// and appends any non-existent trailing components.
func evalPathPreservingTrailing(p string) string {
	if p == "" {
		return ""
	}
	clean := filepath.Clean(p)
	if eval, err := filepath.EvalSymlinks(clean); err == nil {
		return eval
	}
	dir := clean
	var suffix []string
	for {
		parent := filepath.Dir(dir)
		if parent == dir || parent == "." || parent == "/" {
			break
		}
		suffix = append([]string{filepath.Base(dir)}, suffix...)
		if eval, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Join(append([]string{eval}, suffix...)...)
		}
		dir = parent
	}
	return clean
}

// resolvesWithinRoot resolves path components following any existing intermediate symlinks
// and verifies that the resolution remains strictly within root.
func resolvesWithinRoot(root, baseDir, target string) bool {
	if filepath.IsAbs(target) {
		return false
	}
	root = evalPathPreservingTrailing(root)
	baseDir = evalPathPreservingTrailing(baseDir)
	curr := baseDir
	components := strings.Split(filepath.Clean(target), string(filepath.Separator))
	for _, comp := range components {
		if comp == "" || comp == "." {
			continue
		}
		if comp == ".." {
			curr = evalPathPreservingTrailing(curr)
			curr = filepath.Dir(curr)
		} else {
			curr = filepath.Join(curr, comp)
			if fi, err := os.Lstat(curr); err == nil && (fi.Mode()&os.ModeSymlink != 0) {
				eval, err := filepath.EvalSymlinks(curr)
				if err != nil {
					return false
				}
				curr = eval
			}
		}
		rel, err := filepath.Rel(root, curr)
		if err != nil || isPathEscaping(rel) {
			return false
		}
	}
	rel, err := filepath.Rel(root, curr)
	return err == nil && !isPathEscaping(rel)
}
