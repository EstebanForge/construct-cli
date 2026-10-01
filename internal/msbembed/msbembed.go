// Package msbembed ships construct's own msb + libkrunfw pair so microVM
// launches never depend on the host msb installation. The pair is embedded
// at build time (-tags msbembed, see scripts/embed-msb-runtime.sh),
// extracted on first use under <config>/msb-runtime/<sdk-version>, and
// selected via the SDK runtime resolver's environment tier
// (MSB_PATH / MSB_LIBKRUNFW_PATH) plus home isolation (MSB_HOME).
//
// Builds without the tag (local dev, `make build`, unit tests) carry the
// stub variant: Available() is false and every entry point is a no-op, so
// the pre-embed behavior (host msb + ClassifyMsbLaunch gate) stands.
package msbembed

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/EstebanForge/construct-cli/internal/config"
	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// ErrNotEmbedded is returned by Ensure on builds compiled without
// -tags msbembed. Callers fall back to the host-msb launch gate.
var ErrNotEmbedded = errors.New("construct was built without the embedded microsandbox runtime (rebuild with -tags msbembed)")

// Pair is an extracted runtime ready for use.
type Pair struct {
	MsbPath       string
	LibkrunfwPath string
	Dir           string
	Version       string
}

// extractionMarker is the short-circuit record written next to the
// extracted pair. Sizes catch corruption at rest cheaply; full checksums
// are verified against assetChecksums at extraction time only.
type extractionMarker struct {
	Version string `json:"version"`
	MsbSize int64  `json:"msb_size"`
	LibSize int64  `json:"lib_size"`
	MsbSum  string `json:"msb_sha256"`
	LibSum  string `json:"lib_sha256"`
	MsbName string `json:"msb_asset"`
	LibName string `json:"lib_asset"`
}

// Seams for tests.
var (
	rootFn    = func() string { return filepath.Join(config.GetConfigDir(), "msb-runtime") }
	versionFn = msb.SDKVersion
	homeFn    = func() string { return filepath.Join(config.GetConfigDir(), "msb-home") }
)

// Available reports whether this binary embeds the runtime pair.
func Available() bool { return available() }

// Ensure returns the extracted pair, extracting (or re-extracting) it when
// missing, size-mismatched, or left behind by a different SDK version.
func Ensure() (*Pair, error) {
	if !available() {
		return nil, ErrNotEmbedded
	}
	if embeddedMsbName == "" || embeddedLibkrunfwName == "" {
		return nil, errors.New("embedded runtime assets missing for this platform")
	}
	sum, ok := assetChecksums[embeddedMsbName]
	if !ok {
		return nil, fmt.Errorf("embedded runtime manifest has no checksum for %s (build did not run scripts/embed-msb-runtime.sh)", embeddedMsbName)
	}
	libSum, ok := assetChecksums[embeddedLibkrunfwName]
	if !ok {
		return nil, fmt.Errorf("embedded runtime manifest has no checksum for %s (build did not run scripts/embed-msb-runtime.sh)", embeddedLibkrunfwName)
	}
	if err := verifyEmbeddedSums(sum, libSum); err != nil {
		return nil, err
	}

	version := versionFn()
	dir := filepath.Join(rootFn(), version)
	pair := &Pair{
		MsbPath:       filepath.Join(dir, "msb"),
		LibkrunfwPath: filepath.Join(dir, extractedLibName),
		Dir:           dir,
		Version:       version,
	}

	if marker, err := readMarker(dir); err == nil && markerMatches(marker, pair, sum, libSum) {
		return pair, nil
	}
	if err := extractPair(pair, sum, libSum); err != nil {
		return nil, err
	}
	gcOldVersions(version)
	return pair, nil
}

// Activate extracts the pair (idempotent) and pins the process env to it:
// the SDK runtime resolver's environment tier (MSB_PATH,
// MSB_LIBKRUNFW_PATH — both must exist, verified empirically) and the
// isolated home (MSB_HOME) so the catalog DB, image store, and snapshot
// store never touch the user's ~/.microsandbox. Spawned VMM and CLI
// subprocesses inherit the env.
func Activate() error {
	if !available() {
		return nil
	}
	pair, err := Ensure()
	if err != nil {
		return err
	}
	home := homeFn()
	if err := os.MkdirAll(home, 0o755); err != nil {
		return fmt.Errorf("create isolated msb home: %w", err)
	}
	// The resolver reads these from the process env (Environment tier wins
	// by design); setenv on constant keys only fails on invalid names.
	for _, kv := range [][2]string{
		{"MSB_PATH", pair.MsbPath},
		{"MSB_LIBKRUNFW_PATH", pair.LibkrunfwPath},
		{"MSB_HOME", home},
	} {
		if err := os.Setenv(kv[0], kv[1]); err != nil {
			return fmt.Errorf("set %s: %w", kv[0], err)
		}
	}
	return nil
}

// Command returns the msb binary path for CLI-level execs (image pull/ls/
// rm/load): the extracted pair when embedded, else "msb" from PATH.
func Command() (string, error) {
	if !available() {
		return "msb", nil
	}
	pair, err := Ensure()
	if err != nil {
		return "", err
	}
	return pair.MsbPath, nil
}

// MSBHome returns the isolated home directory used when embedded.
func MSBHome() string { return homeFn() }

// verifyEmbeddedSums proves the bytes baked in at build time match the
// manifest the fetch script verified against upstream checksums.
func verifyEmbeddedSums(msbSum, libSum string) error {
	if err := sumMatches(embeddedMsb, msbSum, embeddedMsbName); err != nil {
		return err
	}
	return sumMatches(embeddedLibkrunfw, libSum, embeddedLibkrunfwName)
}

func sumMatches(b []byte, want, name string) error {
	got := sha256.Sum256(b)
	if hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("embedded %s fails its checksum (got %s, want %s); rebuild via scripts/embed-msb-runtime.sh", name, hex.EncodeToString(got[:])[:12], want[:12])
	}
	return nil
}

// extractPair writes the pair at the target dir and records the marker.
// Inputs are already checksum-verified in memory, so plain writes suffice.
func extractPair(pair *Pair, msbSum, libSum string) error {
	if err := os.MkdirAll(pair.Dir, 0o755); err != nil {
		return fmt.Errorf("create runtime dir: %w", err)
	}
	if err := os.WriteFile(pair.MsbPath, embeddedMsb, 0o755); err != nil {
		return fmt.Errorf("extract msb: %w", err)
	}
	if err := os.WriteFile(pair.LibkrunfwPath, embeddedLibkrunfw, 0o755); err != nil {
		return fmt.Errorf("extract libkrunfw: %w", err)
	}
	marker, err := json.Marshal(extractionMarker{
		Version: pair.Version,
		MsbSize: int64(len(embeddedMsb)),
		LibSize: int64(len(embeddedLibkrunfw)),
		MsbSum:  msbSum,
		LibSum:  libSum,
		MsbName: embeddedMsbName,
		LibName: embeddedLibkrunfwName,
	})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(pair.Dir, ".manifest"), marker, 0o644); err != nil {
		return fmt.Errorf("write manifest marker: %w", err)
	}
	return nil
}

// readMarker returns nil when the marker is absent or unreadable; Ensure
// treats that as extract-from-scratch.
func readMarker(dir string) (*extractionMarker, error) {
	data, err := os.ReadFile(filepath.Join(dir, ".manifest"))
	if err != nil {
		return nil, err
	}
	var m extractionMarker
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// markerMatches proves the on-disk pair is exactly what this binary would
// extract: same version, same asset names, non-truncated files.
func markerMatches(m *extractionMarker, pair *Pair, msbSum, libSum string) bool {
	if m == nil || m.Version != pair.Version || m.MsbName != embeddedMsbName || m.LibName != embeddedLibkrunfwName {
		return false
	}
	if m.MsbSum != msbSum || m.LibSum != libSum {
		return false
	}
	msbSt, err := os.Stat(pair.MsbPath)
	if err != nil || msbSt.Size() != m.MsbSize {
		return false
	}
	libSt, err := os.Stat(pair.LibkrunfwPath)
	return err == nil && libSt.Size() == m.LibSize
}

// gcOldVersions removes sibling runtime dirs after a successful extraction
// (the pair is ~70 MB per version).
func gcOldVersions(keep string) {
	entries, err := os.ReadDir(rootFn())
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Name() != keep {
			_ = os.RemoveAll(filepath.Join(rootFn(), e.Name())) //nolint:errcheck // best-effort GC
		}
	}
}
