package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EstebanForge/construct-cli/internal/config"
	msb "github.com/superradcompany/microsandbox/sdk/go"
)

func TestDetectBackendFailClosed(t *testing.T) {
	cfg := config.DefaultConfig()
	_ = &cfg
	cfg.Runtime.Backend = "bogus"
	if _, err := DetectBackend(&cfg); err == nil || !strings.Contains(err.Error(), "unknown runtime backend") {
		t.Fatalf("want unknown-backend error, got %v", err)
	}
	cfg.Runtime.Backend = "docker"
	b, err := DetectBackend(&cfg)
	if err != nil {
		t.Skipf("no container runtime on this host: %v", err)
	}
	if _, ok := b.(*DockerBackend); !ok {
		t.Fatalf("docker backend expected, got %T", b)
	}
}

// TestDetectBackendContainersIgnoresCategoryBinary: "containers" is the
// engine-pool value, never a binary pin. A PATH executable named
// "containers" must not be selected as the runtime.
func TestDetectBackendContainersIgnoresCategoryBinary(t *testing.T) {
	fake := t.TempDir()
	if err := os.WriteFile(filepath.Join(fake, "containers"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("PATH", fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg := config.DefaultConfig()
	cfg.Runtime.Backend = config.BackendContainers
	b, err := DetectBackend(&cfg)
	if err != nil {
		t.Skipf("no container runtime on this host: %v", err)
	}
	if name := b.Name(); name == "containers" {
		t.Fatalf("category value leaked as binary pin: runtime = %q", name)
	}
}

func TestValidateBackendSelected(t *testing.T) {
	cfg := config.DefaultConfig()
	if err := ValidateBackendSelected(&cfg); err != nil {
		t.Fatalf("default backend should validate: %v", err)
	}
	cfg.Runtime.Backend = "docker"
	if err := ValidateBackendSelected(&cfg); err != nil {
		t.Fatalf("docker backend should validate: %v", err)
	}
	cfg.Runtime.Backend = config.BackendMsb
	if err := ValidateBackendSelected(&cfg); err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("msb should fail closed on compose-only paths, got %v", err)
	}
	cfg.Runtime.Backend = "bogus"
	if err := ValidateBackendSelected(&cfg); err == nil {
		t.Fatal("unknown backend should fail")
	}
}

func TestMsbBackendAvailable(t *testing.T) {
	m := NewMsbBackend()
	if ok, err := m.Available(context.TODO()); err != nil {
		t.Fatalf("Available error: %v", err)
	} else if !ok {
		t.Skip("msb not installed")
	}
}

// TestManifestDigestForHost: the registry digest resolver must return the
// linux/<host-arch> platform manifest digest on multi-arch tags (msb
// caches the platform manifest, never the index), skip unknown-platform
// attestation entries, and fall back to "" (keep cached) on anything it
// cannot resolve — a wrong answer here re-downloads the image on every
// create.
func TestManifestDigestForHost(t *testing.T) {
	indexJSON := `{"manifests":[
		{"digest":"sha256:attest0000000000000000000000000000000000000000000000000000000000","platform":{"architecture":"unknown","os":"unknown"}},
		{"digest":"sha256:linuxamd640000000000000000000000000000000000000000000000000000000","platform":{"architecture":"amd64","os":"linux"}},
		{"digest":"sha256:linuxarm640000000000000000000000000000000000000000000000000000000","platform":{"architecture":"arm64","os":"linux","variant":"v8"}},
		{"digest":"sha256:macarm6400000000000000000000000000000000000000000000000000000000","platform":{"architecture":"arm64","os":"darwin"}}
	]}`
	const indexType = "application/vnd.oci.image.index.v1+json"
	const manifestType = "application/vnd.oci.image.manifest.v1+json"
	const topDigest = "sha256:indextop000000000000000000000000000000000000000000000000000000000"
	const amd64Digest = "sha256:linuxamd640000000000000000000000000000000000000000000000000000000"
	const arm64Digest = "sha256:linuxarm640000000000000000000000000000000000000000000000000000000"

	cases := []struct {
		name        string
		contentType string
		body        string
		goarch      string
		want        string
	}{
		{"amd64 host picks linux/amd64", indexType, indexJSON, "amd64", amd64Digest},
		{"arm64 host picks linux/arm64 ignoring darwin and variant", indexType, indexJSON, "arm64", arm64Digest},
		{"unknown arch resolves to nothing", indexType, indexJSON, "riscv64", ""},
		{"single-arch manifest keeps top digest", manifestType, indexJSON, "amd64", topDigest},
		{"docker manifest list also resolves", "application/vnd.docker.distribution.manifest.list.v2+json", indexJSON, "amd64", amd64Digest},
		{"content type match is case-insensitive", "Application/VND.OCI.Image.Index.V1+JSON", indexJSON, "amd64", amd64Digest},
		{"garbage index body keeps cached", indexType, "{not json", "amd64", ""},
		{"index without any linux entry keeps cached", indexType, `{"manifests":[{"digest":"sha256:x","platform":{"architecture":"amd64","os":"windows"}}]}`, "amd64", ""},
		{"empty body keeps cached", indexType, "", "amd64", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := manifestDigestForHost(tc.contentType, []byte(tc.body), topDigest, tc.goarch)
			if got != tc.want {
				t.Fatalf("manifestDigestForHost = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestEnsureImagePrefersLocalBuild: a localhost/ ref only lands via an
// explicit local build + transition (`construct sys rebuild` / `sys init`
// on this backend), so EnsureImage must adopt it WITHOUT consulting GHCR —
// even when a stale GHCR ref is ALSO cached (the normal state after any
// prior run pulled from the registry). The digest drift check would see a
// mismatch for a local build and re-pull the stale published image over
// the fresh one. Proven by the early nil return: without the preference
// the flow falls through to pull / fallback / build-confirm and fails
// closed on this exec-less test host.
func TestEnsureImagePrefersLocalBuild(t *testing.T) {
	origCached := msbImageCachedFn
	msbImageCachedFn = func(ref string) bool {
		return ref == "localhost/construct-box:latest" || ref == "ghcr.io/estebanforge/construct-box:latest"
	}
	t.Cleanup(func() { msbImageCachedFn = origCached })

	cfg := config.DefaultConfig()
	ref, err := (&MsbBackend{}).EnsureImage(&cfg)
	if err != nil {
		t.Fatalf("EnsureImage must adopt the cached local build, got: %v", err)
	}
	if ref != "localhost/construct-box:latest" {
		t.Fatalf("EnsureImage must return the local build ref for the boot spec, got %q", ref)
	}
}

// TestEnsureImageCachedDigestSkipsPull: when the GHCR ref is cached and the
// registry digest matches (or is unverifiable), msb pull is a guaranteed
// no-op — it never re-resolves a cached tag — so EnsureImage must print the
// ready line WITHOUT spawning a pull or the "Pulling" download line. Proven
// by the stubbed pull error: ANY reach of the pull block fails the test on
// every host, exec-less or not.
func TestEnsureImageCachedDigestSkipsPull(t *testing.T) {
	origDigest, origRemote, origCached := msbImageDigestFn, ghcrRemoteDigestFn, msbImageCachedFn
	origRun := runMsbCmd
	t.Cleanup(func() {
		msbImageDigestFn, ghcrRemoteDigestFn, msbImageCachedFn = origDigest, origRemote, origCached
		runMsbCmd = origRun
	})

	cases := []struct {
		name   string
		remote string
	}{
		{"digest matches registry", "sha256:same"},
		{"registry digest unverifiable (offline)", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msbImageDigestFn = func(string) string { return "sha256:same" }
			ghcrRemoteDigestFn = func(string) string { return tc.remote }
			msbImageCachedFn = func(ref string) bool { return ref == PrepullImageRef }
			runMsbCmd = func(string, ...string) ([]byte, error) {
				return nil, errors.New("stub: pull block must not run when the ref is cached")
			}

			cfg := config.DefaultConfig()
			ref, err := (&MsbBackend{}).EnsureImage(&cfg)
			if err != nil {
				t.Fatalf("cached ref must short-circuit to ready without a pull, got: %v", err)
			}
			if ref != PrepullImageRef {
				t.Fatalf("cached ref must return the verified GHCR ref for the boot spec, got %q", ref)
			}
		})
	}
}

// TestEnsureImageDigestDriftFallsThroughToPull: a digest mismatch must fall
// through to the pull (after the rm), never short-circuit to ready. Proven
// by the fail-closed error at the END of the flow: drift → rm → pull error
// (hermetic seam; the rm+pull execs hit a LIVE msb store on dev hosts if
// left real) → cached fallback stubbed empty → local image stubbed absent →
// build confirm declined by the stubbed prompt.
func TestEnsureImageDigestDriftFallsThroughToPull(t *testing.T) {
	origDigest, origRemote := msbImageDigestFn, ghcrRemoteDigestFn
	origCached, origLocalExists := msbImageCachedFn, localConstructImageExistsFn
	origTTY, origConfirm := stdinIsTerminal, confirmPrompt
	origRun := runMsbCmd
	t.Cleanup(func() {
		msbImageDigestFn, ghcrRemoteDigestFn = origDigest, origRemote
		msbImageCachedFn, localConstructImageExistsFn = origCached, origLocalExists
		stdinIsTerminal, confirmPrompt = origTTY, origConfirm
		runMsbCmd = origRun
	})

	msbImageDigestFn = func(string) string { return "sha256:old" }
	ghcrRemoteDigestFn = func(string) string { return "sha256:new" }
	msbImageCachedFn = func(string) bool { return false } // localhost check AND imageLoaded fallback
	localConstructImageExistsFn = func(*config.Config) bool { return false }
	stdinIsTerminal = func() bool { return true }
	confirmPrompt = func(string) bool { return false } // declined → fail-closed error
	runMsbCmd = func(string, ...string) ([]byte, error) { return nil, errors.New("stub: pull refused") }

	cfg := config.DefaultConfig()
	if _, err := (&MsbBackend{}).EnsureImage(&cfg); err == nil {
		t.Fatal("digest drift must fall through to the pull, not short-circuit to ready")
	}
}

// TestEnsureImageDriftUsesForcePull: the daemon sandbox pins the GHCR ref
// in the msb store, so the old unforced rm+pull pair never refreshed it —
// rm was refused (error discarded) and the plain pull no-ops on any cached
// ref. The drift refresh must pass --force to pull and verify the store
// digest actually moved before reporting ready.
func TestEnsureImageDriftUsesForcePull(t *testing.T) {
	origDigest, origRemote := msbImageDigestFn, ghcrRemoteDigestFn
	origCached := msbImageCachedFn
	origRun := runMsbCmd
	t.Cleanup(func() {
		msbImageDigestFn, ghcrRemoteDigestFn = origDigest, origRemote
		msbImageCachedFn = origCached
		runMsbCmd = origRun
	})

	digestReads := 0
	msbImageDigestFn = func(string) string {
		digestReads++
		if digestReads == 1 {
			return "sha256:old" // drift probe before the pull
		}
		return "sha256:new" // store digest after the forced re-download
	}
	ghcrRemoteDigestFn = func(string) string { return "sha256:new" }
	msbImageCachedFn = func(ref string) bool { return ref == PrepullImageRef }

	var pullArgs []string
	runMsbCmd = func(_ string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "pull" {
			pullArgs = args
		}
		return nil, nil
	}

	cfg := config.DefaultConfig()
	ref, err := (&MsbBackend{}).EnsureImage(&cfg)
	if err != nil || ref != PrepullImageRef {
		t.Fatalf("drift refresh must land the registry ref, got ref=%q err=%v", ref, err)
	}
	if len(pullArgs) == 0 {
		t.Fatal("drift must reach the pull")
	}
	forced := false
	for _, a := range pullArgs[1:] {
		if a == "--force" || a == "-f" {
			forced = true
		}
	}
	if !forced {
		t.Fatalf("drift pull must pass --force (unforced rm is refused on a pinned ref), got args %v", pullArgs)
	}
	if digestReads < 2 {
		t.Fatalf("post-pull store digest must be re-verified, read %d time(s)", digestReads)
	}
}

// TestMsbRecreateGate: a needed recreate (fork or cold) tears the daemon
// down, killing every attached session. With zero sessions it never
// interferes; a learned root cannot defer (the run needs its mount) and
// fails busy; config/image drift rides the current daemon and recreates
// on the next idle run.
func TestMsbRecreateGate(t *testing.T) {
	if d, err := msbRecreateGate("workspace roots changed (learned root added, removed, or evicted)", "~/proj", 0); d || err != nil {
		t.Fatalf("zero sessions must never gate: deferred=%v err=%v", d, err)
	}
	d, err := msbRecreateGate("workspace roots changed (learned root added, removed, or evicted)", "~/proj", 2)
	if d || !errors.Is(err, ErrMsbDaemonBusy) {
		t.Fatalf("learned root with live sessions must fail busy, got deferred=%v err=%v", d, err)
	}
	d, err = msbRecreateGate("embedded msb runtime changed (construct upgrade)", "", 1)
	if !d || err != nil {
		t.Fatalf("drift with live sessions must defer without error, got deferred=%v err=%v", d, err)
	}
	if d, err = msbRecreateGate("embedded msb runtime changed (construct upgrade)", "", 0); d || err != nil {
		t.Fatalf("drift with zero sessions must proceed: deferred=%v err=%v", d, err)
	}
}

// TestGetMsbWorkspaceMountDestCollision: two roots with the same basename
// used to share one guest mount (/workspaces/<name>), so the second root
// in sort order silently won and sessions ran in the wrong tree. The dest
// now carries a path hash: unique per root, stable across runs, still
// readable.
func TestGetMsbWorkspaceMountDestCollision(t *testing.T) {
	work := filepath.Join(t.TempDir(), "api")
	oss := filepath.Join(t.TempDir(), "api")
	for _, d := range []string{work, oss} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a, b := GetMsbWorkspaceMountDest(work), GetMsbWorkspaceMountDest(oss)
	if a == b {
		t.Fatalf("same-basename roots must not collide on %q", a)
	}
	if GetMsbWorkspaceMountDest(work) != a {
		t.Fatal("dest must be stable across calls")
	}
	if !strings.HasPrefix(a, "/workspaces/api-") {
		t.Fatalf("dest must keep the readable base prefix, got %q", a)
	}
	if GetMsbWorkspaceMountDest("") != "/workspaces" {
		t.Fatal("empty dir must map to the bare /workspaces root")
	}
}

// TestMapHostWorkdirToGuest: the workdir mapper must resolve by
// longest-prefix over the actual mount maps, disambiguate same-basename
// roots, refuse the home bind as a workdir, and fail closed on unmapped
// dirs instead of landing blind in /workspaces.
func TestMapHostWorkdirToGuest(t *testing.T) {
	maps := []MsbPathMap{
		{Guest: "/home/construct", Host: "/h/u/.config/construct-cli/home"},
		{Guest: "/workspaces/api-11111111", Host: "/h/u/work/api"},
		{Guest: "/workspaces/api-22222222", Host: "/h/u/oss/api"},
	}

	got, err := MapHostWorkdirToGuest(maps, "/h/u/work/api")
	if err != nil || got != "/workspaces/api-11111111" {
		t.Fatalf("root itself: got %q err=%v", got, err)
	}
	got, err = MapHostWorkdirToGuest(maps, "/h/u/work/api/cmd/x")
	if err != nil || got != "/workspaces/api-11111111/cmd/x" {
		t.Fatalf("subfolder: got %q err=%v", got, err)
	}
	got, err = MapHostWorkdirToGuest(maps, "/h/u/oss/api/cmd/x")
	if err != nil || got != "/workspaces/api-22222222/cmd/x" {
		t.Fatalf("same-basename sibling must resolve to its own root: got %q err=%v", got, err)
	}
	if _, err = MapHostWorkdirToGuest(maps, "/h/u/.config/construct-cli/home/x"); err == nil {
		t.Fatal("the home bind must not host a workdir")
	}
	if _, err = MapHostWorkdirToGuest(maps, "/h/u/other"); !errors.Is(err, ErrMsbDaemonWorkdirUnmapped) {
		t.Fatalf("unmapped dir must fail closed with ErrMsbDaemonWorkdirUnmapped, got %v", err)
	}
	if _, err = MapHostWorkdirToGuest(nil, "/h/u/work/api"); err == nil {
		t.Fatal("empty map set must fail closed")
	}
}

// TestImageInspectMemo: `msb image inspect` results are memoized per ref
// for the process lifetime (drift decision, candidate walk, and verify
// otherwise spawn 3-5 inspects per run); bust forces re-inspection.
func TestImageInspectMemo(t *testing.T) {
	origDigest, origCached := inspectDigestFn, inspectCachedFn
	t.Cleanup(func() {
		inspectDigestFn, inspectCachedFn = origDigest, origCached
		bustImageInspectMemo()
	})
	bustImageInspectMemo()

	digestCalls, cachedCalls := 0, 0
	inspectDigestFn = func(string) string { digestCalls++; return "sha256:aa" }
	inspectCachedFn = func(string) bool { cachedCalls++; return true }

	for range 2 {
		if got := msbImageDigest("ref-a"); got != "sha256:aa" {
			t.Fatalf("digest = %q", got)
		}
	}
	if digestCalls != 1 {
		t.Fatalf("digest inspect ran %d times, want 1 (memoized)", digestCalls)
	}
	for range 2 {
		if !msbImageCached("ref-a") {
			t.Fatal("cached = false")
		}
	}
	if cachedCalls != 1 {
		t.Fatalf("cached inspect ran %d times, want 1 (memoized)", cachedCalls)
	}
	_ = msbImageDigest("ref-b")
	if digestCalls != 2 {
		t.Fatalf("a second ref must not hit ref-a's memo entry: %d calls", digestCalls)
	}

	bustImageInspectMemo()
	_ = msbImageDigest("ref-a")
	if digestCalls != 3 {
		t.Fatalf("bust must force re-inspection: %d calls", digestCalls)
	}
}

// TestEnsureImageDriftVerifySeesPostPullDigest pins the bust: the drift
// decision memoizes the pre-pull store digest, and the post-pull verify
// must re-inspect or a successful refresh would read as its old digest.
func TestEnsureImageDriftVerifySeesPostPullDigest(t *testing.T) {
	origInspect, origCached, origFetch, origRun := inspectDigestFn, msbImageCachedFn, ghcrFetchDigest, runMsbCmd
	t.Cleanup(func() {
		inspectDigestFn, msbImageCachedFn, ghcrFetchDigest, runMsbCmd = origInspect, origCached, origFetch, origRun
		bustImageInspectMemo()
	})
	t.Setenv("HOME", t.TempDir()) // isolate the remote-digest cache file
	bustImageInspectMemo()

	pulled := false
	inspectCalls := 0
	inspectDigestFn = func(string) string {
		inspectCalls++
		if pulled {
			return "sha256:new"
		}
		return "sha256:old"
	}
	ghcrFetchDigest = func(string) string { return "sha256:new" }
	msbImageCachedFn = func(ref string) bool { return ref == PrepullImageRef }
	runMsbCmd = func(_ string, _ ...string) ([]byte, error) {
		pulled = true
		return nil, nil
	}

	cfg := config.DefaultConfig()
	ref, err := (&MsbBackend{}).EnsureImage(&cfg)
	if err != nil || ref != PrepullImageRef {
		t.Fatalf("EnsureImage = %q, %v", ref, err)
	}
	if !pulled {
		t.Fatal("drift must force a pull")
	}
	if inspectCalls < 2 {
		t.Fatalf("post-pull verify must re-inspect past the memo: %d inspect calls", inspectCalls)
	}
}

// TestMsbDaemonNeedsRecreateEnvDrift: the create-time env hash label is
// checked like the sudo/skills labels — a [network] or env toggle must
// recreate the daemon or the old env keeps applying.
func TestMsbDaemonNeedsRecreateEnvDrift(t *testing.T) {
	cfg := config.DefaultConfig()
	dm := DaemonMounts{Enabled: true, Hash: "h"}
	clean := func(cfg *config.Config) map[string]string {
		return map[string]string{
			DaemonSkillsLabelKey:     SkillsDaemonHash(cfg),
			DaemonSudoLabelKey:       sudoPolicy(cfg),
			DaemonSDKVersionLabelKey: msb.SDKVersion(),
			DaemonEnvHashLabelKey:    msbEnvHash(cfg),
			DaemonMountsLabelKey:     "h",
		}
	}

	need, reason := msbDaemonNeedsRecreate(dm, clean(&cfg), "{}", "", false, &cfg, "")
	if need {
		t.Fatalf("clean labels must reuse the daemon, got reason %q", reason)
	}

	drifted := config.DefaultConfig()
	drifted.Network.Mode = "offline"
	labels := clean(&cfg) // daemon still carries the old env
	need, reason = msbDaemonNeedsRecreate(dm, labels, "{}", "", false, &drifted, "")
	if !need {
		t.Fatal("a network mode change must recreate the daemon")
	}
	if !strings.Contains(reason, "network") {
		t.Fatalf("reason %q must name the env drift", reason)
	}
}

// TestMsbDaemonNeedsRecreateCollectsAllReasons: drift evaluation must not
// stop at the first drifted label — a fork decision taken on the first
// reason alone would mask image/sudo drift behind a forkable one.
func TestMsbDaemonNeedsRecreateCollectsAllReasons(t *testing.T) {
	cfg := config.DefaultConfig()
	labels := map[string]string{
		DaemonSkillsLabelKey:     "stale",
		DaemonSudoLabelKey:       "stale",
		DaemonSDKVersionLabelKey: msb.SDKVersion(),
		DaemonEnvHashLabelKey:    msbEnvHash(&cfg),
		DaemonMountsLabelKey:     "stale",
	}
	dm := DaemonMounts{Enabled: true, Hash: "h"}
	need, reason := msbDaemonNeedsRecreate(dm, labels, "{}", "", false, &cfg, "")
	if !need {
		t.Fatal("stale labels must recreate")
	}
	for _, fragment := range []string{"skills", "sudo", "mount_paths"} {
		if !strings.Contains(reason, fragment) {
			t.Errorf("joined reason %q missing %q", reason, fragment)
		}
	}
}

// TestMsbRecreateForkableJoinedReasons: a joined reason is forkable only
// when EVERY part is forkable — forking preserves the old disk, so one
// non-forkable drift (image, sudo) must force the full recreate.
func TestMsbRecreateForkableJoinedReasons(t *testing.T) {
	forkable := "daemon.mount_paths changed; workspace roots changed (learned root added, removed, or evicted)"
	mixed := "host skills mounts changed (source, mode, or targets); construct-box image changed"
	if !msbRecreateForkable(forkable) {
		t.Error("all-forkable parts must stay forkable")
	}
	if msbRecreateForkable(mixed) {
		t.Errorf("mixed reasons must not fork (forking would carry the drifted disk)")
	}
	if msbRecreateForkable("construct-box image changed") {
		t.Error("image drift alone must not fork")
	}
}

// TestDNSRuleGatewayScoped: the DNS allow must be gateway-scoped (the SDK
// factory's UDP+TCP 53); a bare UDP/53 rule with no destination is a raw
// UDP exfil channel to any host.
func TestDNSRuleGatewayScoped(t *testing.T) {
	got, want := dnsRule(), msb.Rule.AllowDNS()
	protosEqual := len(got.Protocols) == len(want.Protocols)
	for i := range got.Protocols {
		if got.Protocols[i] != want.Protocols[i] {
			protosEqual = false
		}
	}
	if got.Action != want.Action || got.Direction != want.Direction || got.Destination != want.Destination ||
		got.Port != want.Port || !protosEqual {
		t.Errorf("dnsRule = %+v, want the SDK gateway-scoped AllowDNS rule %+v", got, want)
	}
}

// TestMsbConstructImageRefPrefersLocalBuildOverGhcr: when both the stale
// GHCR ref and a deliberate local build are cached, the boot image and
// daemon digest label must resolve to the LOCAL build. The candidates
// list used to order the registry ref first, so the sandbox kept booting
// the stale GHCR image even after a successful local build + transition.
func TestMsbConstructImageRefPrefersLocalBuildOverGhcr(t *testing.T) {
	origCached := msbImageCachedFn
	msbImageCachedFn = func(ref string) bool {
		return ref == "localhost/construct-box:latest" || ref == "ghcr.io/estebanforge/construct-box:latest"
	}
	t.Cleanup(func() { msbImageCachedFn = origCached })

	if got := MsbConstructImageRef(); got != "localhost/construct-box:latest" {
		t.Fatalf("MsbConstructImageRef() = %q, want the deliberate local build ref", got)
	}
}

// TestTransitionLocalConstructImageToMsbNoLocalImage: without a locally
// built docker image the transition is a no-op success — the GHCR pull
// remains the acquisition path and `sys rebuild` must not fail on a
// machine whose docker store has no construct-box image.
func TestTransitionLocalConstructImageToMsbNoLocalImage(t *testing.T) {
	orig := localConstructImageExistsFn
	localConstructImageExistsFn = func(*config.Config) bool { return false }
	t.Cleanup(func() { localConstructImageExistsFn = orig })

	cfg := config.DefaultConfig()
	if err := TransitionLocalConstructImageToMsb(&cfg); err != nil {
		t.Fatalf("no local image must no-op with nil, got: %v", err)
	}
}

// TestMsbNetworkPublicDenyCap pins the public-mode rule shape: msb 0.7.8
// treats DefaultEgress allow as opening EVERY host port (live-probed:
// an unlisted bound port is reachable with per-port rules alone), so
// public modes must end their rules with a first-match deny cap after
// the per-port allows and DNS, keeping the allows a whitelist. Offline
// keeps the bare additive shape: its deny default already scopes.
func TestMsbNetworkPublicDenyCap(t *testing.T) {
	isDenyHostAny := func(r msb.PolicyRule, proto msb.PolicyProtocol) bool {
		return r.Action == msb.PolicyActionDeny &&
			r.Direction == msb.PolicyDirectionEgress &&
			r.Destination == "host" &&
			r.Protocol == proto &&
			r.Port == ""
	}
	for _, mode := range []string{"permissive", "strict"} {
		net := msbNetworkPublic([]int{40001})
		if len(net.Rules) < 5 {
			t.Fatalf("%s: want allows + dns + tcp cap + udp cap + private allow, got %d rules", mode, len(net.Rules))
		}
		tcpCapIdx := -1
		udpCapIdx := -1
		privateIdx := -1
		dnsIdx := -1
		for i, r := range net.Rules {
			if r.Destination == "host" && r.Port == "53" {
				dnsIdx = i
			}
			if isDenyHostAny(r, msb.PolicyProtocolTCP) {
				tcpCapIdx = i
			}
			if isDenyHostAny(r, msb.PolicyProtocolUDP) {
				udpCapIdx = i
			}
			if r.Action == msb.PolicyActionAllow && r.Destination == "private" {
				privateIdx = i
			}
		}
		if tcpCapIdx < 0 || udpCapIdx < 0 {
			t.Fatalf("%s: missing host TCP/UDP deny caps", mode)
		}
		if privateIdx < 0 {
			t.Fatalf("%s: missing private LAN allow rule", mode)
		}
		// DNS must precede the caps so first-match keeps 53 allowed.
		if dnsIdx < 0 || dnsIdx >= tcpCapIdx {
			t.Errorf("%s: dns rule must precede the deny caps (dns: %d, tcpCap: %d)", mode, dnsIdx, tcpCapIdx)
		}
		// Deny caps must precede private LAN allow so host gateway IPs cannot bypass deny cap.
		if tcpCapIdx >= privateIdx || udpCapIdx >= privateIdx {
			t.Errorf("%s: host deny caps must precede private LAN allow (tcpCap: %d, udpCap: %d, private: %d)", mode, tcpCapIdx, udpCapIdx, privateIdx)
		}
	}

	offline := msbNetworkOffline([]int{40001})
	for _, r := range offline.Rules {
		if r.Action == msb.PolicyActionDeny {
			t.Errorf("offline rules must stay additive allows, got deny %+v", r)
		}
	}
}
