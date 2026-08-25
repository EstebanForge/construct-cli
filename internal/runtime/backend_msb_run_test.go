package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/EstebanForge/construct-cli/internal/config"
)

func TestBuildMsbRunSpecMounts(t *testing.T) {
	// The home bind only exists when the host construct home is present.
	// Isolate HOME and create it so the test does not depend on machine
	// state (CI runners have no ~/.config/construct-cli/home).
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".config", "construct-cli", "home"), 0o755); err != nil {
		t.Fatalf("create construct home: %v", err)
	}

	cfg := config.DefaultConfig()
	spec := BuildMsbRunSpec(&cfg, "sb-test", "/tmp/proj", []int{18080})
	if spec.Name != "sb-test" {
		t.Errorf("Name = %q", spec.Name)
	}
	if spec.Image != "construct-box:latest" {
		t.Errorf("Image = %q", spec.Image)
	}
	if mounts := spec.Mounts; mounts["/home/linuxbrew/.linuxbrew"].Named != "" {
		t.Error("linuxbrew must not be volume-backed (shadows image brew)")
	}
	if mounts := spec.Mounts; mounts[msbHomeMountDest].Bind == "" {
		t.Errorf("home bind mount missing: %+v", mounts[msbHomeMountDest])
	}
	if spec.Mounts["/workspaces/proj"].Bind != "/tmp/proj" {
		t.Errorf("project mount missing: %+v", spec.Mounts["/workspaces/proj"])
	}
	if spec.HostAliasEnv != msbHostAlias || spec.Env["CONSTRUCT_HOST_ALIAS"] != msbHostAlias {
		t.Errorf("host alias env missing: %+v", spec.Env)
	}
	if spec.Env["CONSTRUCT_LOOPBACK_PORTS"] != "80,443" {
		t.Errorf("expected default loopback ports 80,443 in env, got: %q", spec.Env["CONSTRUCT_LOOPBACK_PORTS"])
	}
}

// Without a host construct home the bind is absent and /home/construct
// falls back to the image's baked-in skeleton (msbHostConstructHome "").
func TestBuildMsbRunSpecMountsWithoutHostHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.DefaultConfig()
	spec := BuildMsbRunSpec(&cfg, "sb", "/tmp/proj", nil)
	if m := spec.Mounts[msbHomeMountDest]; m.Bind != "" {
		t.Errorf("home bind must be absent without a host construct home: %+v", m)
	}
}

func TestBuildMsbRunSpecNetwork(t *testing.T) {
	cfg := config.DefaultConfig()

	// Offline: deny-by-default, host bridge + DNS allowed.
	cfg.Network.Mode = "offline"
	spec := BuildMsbRunSpec(&cfg, "sb", "", []int{18080})
	if spec.Network.DefaultEgress == "allow" {
		t.Error("offline mode must not allow default egress")
	}
	if !hasHostRule(spec.Network, "18080") {
		t.Error("offline mode must keep host bridge rule")
	}

	// Permissive: default allow.
	cfg.Network.Mode = "permissive"
	spec = BuildMsbRunSpec(&cfg, "sb", "", []int{18080, 443})
	if spec.Network.DefaultEgress != "allow" {
		t.Errorf("permissive default egress = %q", spec.Network.DefaultEgress)
	}
	if !hasHostRule(spec.Network, "443") {
		t.Error("permissive mode missing host bridge rule for 443")
	}
}

func TestMsbRunSpecEntrypointDefault(t *testing.T) {
	cfg := config.DefaultConfig()
	spec := BuildMsbRunSpec(&cfg, "sb", "", nil)
	if len(spec.Entrypoint) != 0 {
		t.Errorf("default spec must keep the image entrypoint, got %v", spec.Entrypoint)
	}
}

func TestMsbPathMapsMirrorSandboxMounts(t *testing.T) {
	proj := t.TempDir()
	mounts := msbSandboxMounts(nil, proj)
	paths := MsbPathMaps(nil, proj)
	if len(paths) != len(mounts) {
		t.Fatalf("MsbPathMaps (%d) must cover every msbSandboxMounts entry (%d)", len(paths), len(mounts))
	}
	for _, pm := range paths {
		mc, ok := mounts[pm.Guest]
		if !ok {
			t.Errorf("MsbPathMaps guest %q not in msbSandboxMounts", pm.Guest)
			continue
		}
		if mc.Kind() != msb.MountKindBind || mc.Bind != pm.Host {
			t.Errorf("guest %q: PathMap host %q != mount bind %q", pm.Guest, pm.Host, mc.Bind)
		}
	}
	// Project dir must always be present.
	expectedGuest := GetMsbWorkspaceMountDest(proj)
	found := false
	for _, pm := range paths {
		if pm.Guest == expectedGuest {
			found = true
		}
	}
	if !found {
		t.Errorf("MsbPathMaps missing %s project translation", expectedGuest)
	}
}

func msbTestConfigWithMountPaths(t *testing.T, paths ...string) config.Config {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Daemon.MultiPathsEnabled = true
	cfg.Daemon.MountPaths = paths
	return cfg
}

// Multi-path mode mounts every configured root and never adds an extra
// project bind: the mount set must equal config exactly (hash parity with
// the daemon label checked in EnsureMsbDaemon).
func TestMsbSandboxMountsMultiPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".config", "construct-cli", "home"), 0o755); err != nil {
		t.Fatalf("create construct home: %v", err)
	}

	rootA, rootB := t.TempDir(), t.TempDir()
	cfg := msbTestConfigWithMountPaths(t, rootA, rootB)

	mounts := msbSandboxMounts(&cfg, t.TempDir())
	if len(mounts) != 3 { // home + two configured roots
		t.Fatalf("expected home + 2 configured roots, got %d mounts: %v", len(mounts), mounts)
	}
	dm := ResolveDaemonMounts(&cfg)
	if !dm.Enabled {
		t.Fatal("expected daemon mounts enabled")
	}
	for _, m := range dm.Mounts {
		mc, ok := mounts[m.ContainerPath]
		if !ok || mc.Bind != m.HostPath {
			t.Errorf("configured root %s not mounted at %s", m.HostPath, m.ContainerPath)
		}
	}
}

func TestMsbPathMapsMultiPathMirror(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".config", "construct-cli", "home"), 0o755); err != nil {
		t.Fatalf("create construct home: %v", err)
	}

	rootA, rootB := t.TempDir(), t.TempDir()
	cfg := msbTestConfigWithMountPaths(t, rootA, rootB)

	mounts := msbSandboxMounts(&cfg, "")
	paths := MsbPathMaps(&cfg, "")
	if len(paths) != len(mounts) {
		t.Fatalf("MsbPathMaps (%d) must cover every msbSandboxMounts entry (%d)", len(paths), len(mounts))
	}
	for _, pm := range paths {
		mc, ok := mounts[pm.Guest]
		if !ok {
			t.Errorf("MsbPathMaps guest %q not in msbSandboxMounts", pm.Guest)
			continue
		}
		if mc.Kind() != msb.MountKindBind || mc.Bind != pm.Host {
			t.Errorf("guest %q: PathMap host %q != mount bind %q", pm.Guest, pm.Host, mc.Bind)
		}
	}
}

func TestBuildMsbRunSpecMountsHashLabel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	cfg := msbTestConfigWithMountPaths(t, root)

	spec := BuildMsbRunSpec(&cfg, "sb", t.TempDir(), nil)
	dm := ResolveDaemonMounts(&cfg)
	if spec.Labels[DaemonMountsLabelKey] != dm.Hash {
		t.Errorf("mounts hash label = %q, want %q", spec.Labels[DaemonMountsLabelKey], dm.Hash)
	}

	single := config.DefaultConfig()
	specSingle := BuildMsbRunSpec(&single, "sb", "", nil)
	if _, ok := specSingle.Labels[DaemonMountsLabelKey]; ok {
		t.Error("single-path spec must not carry the mounts hash label")
	}
}

func TestMsbDaemonNeedsRecreate(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	other := t.TempDir()

	dest := GetMsbWorkspaceMountDest(root)
	cfgJSON := fmt.Sprintf(`{"mounts":[{"type":"Bind","host":%q,"guest":%q}]}`, root, dest)
	singleLabels := map[string]string{"construct.project_dir": root}

	multi := DaemonMounts{Enabled: true, Hash: "abc", Mounts: []DaemonMount{{HostPath: root, ContainerPath: "/workspaces/x"}}}
	multiLabels := map[string]string{DaemonMountsLabelKey: "abc"}

	tests := []struct {
		name       string
		dm         DaemonMounts
		labels     map[string]string
		cfgJSON    string
		projectDir string
		want       bool
	}{
		{"multi hash match reuses", multi, multiLabels, "", sub, false},
		{"multi hash mismatch recreates", multi, map[string]string{DaemonMountsLabelKey: "zzz"}, "", sub, true},
		{"single exact root reuses", DaemonMounts{}, singleLabels, cfgJSON, root, false},
		{"single subdir reuses", DaemonMounts{}, singleLabels, cfgJSON, sub, false},
		{"single other root recreates", DaemonMounts{}, singleLabels, cfgJSON, other, true},
		{"single missing label recreates", DaemonMounts{}, map[string]string{}, cfgJSON, root, true},
		{"single mount drift recreates", DaemonMounts{}, singleLabels, `{"mounts":[]}`, root, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := msbDaemonNeedsRecreate(tt.dm, tt.labels, tt.cfgJSON, tt.projectDir, false)
			if got != tt.want {
				t.Fatalf("needRecreate = %v (reason %q), want %v", got, reason, tt.want)
			}
			if got && reason == "" {
				t.Error("recreate decision must carry a reason")
			}
		})
	}
}

func TestMsbNetworkEmptyPortsAnyPortHostRule(t *testing.T) {
	// No known bridge ports (engine binds random ports per run): a single
	// any-port host-TCP rule must be emitted so bridges stay reachable.
	for _, mode := range []string{"offline", "permissive"} {
		net := msbNetworkConfig(mode, nil)
		hostRules := 0
		for _, r := range net.Rules {
			if r.Destination == "host" {
				hostRules++
				if r.Port != "" {
					t.Errorf("%s: empty ports must yield any-port host rule, got port %q", mode, r.Port)
				}
				if r.Protocol != msb.PolicyProtocolTCP {
					t.Errorf("%s: host rule must be TCP, got %q", mode, r.Protocol)
				}
			}
		}
		if hostRules == 0 {
			t.Errorf("%s: no host transport rule emitted", mode)
		}
	}
}

func hasHostRule(net *msb.NetworkConfig, port string) bool {
	for _, r := range net.Rules {
		if r.Destination == "host" && r.Port == port && r.Action == "allow" {
			return true
		}
	}
	return false
}

func TestEnvSliceToMap(t *testing.T) {
	got := envSliceToMap([]string{"A=1", "B=2", "A=3", "MALFORMED"})
	if got["A"] != "3" {
		t.Errorf("later assignment must win: A=%q", got["A"])
	}
	if got["B"] != "2" {
		t.Errorf("B = %q", got["B"])
	}
	if _, ok := got["MALFORMED"]; ok {
		t.Errorf("env map = %+v", got)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
}

func TestResolveExecUserMsb(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Sandbox.ExecAsHostUser = true
	user := ResolveExecUserMsb(&cfg)
	if user != "construct" {
		t.Errorf("expected construct, got %q", user)
	}
}

func TestGetMsbWorkspaceMountDest(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "/workspaces"},
		{"/", "/workspaces"},
		{".", "/workspaces"},
		{"/Users/esteban/Dev/my-proj", "/workspaces/my-proj"},
		{"/Users/esteban/Dev/my-proj/", "/workspaces/my-proj"},
		{"/tmp/workspace", "/workspaces/workspace"},
		{"/tmp/nested/deep/project", "/workspaces/project"},
	}

	for _, tt := range tests {
		got := GetMsbWorkspaceMountDest(tt.input)
		if got != tt.expected {
			t.Errorf("GetMsbWorkspaceMountDest(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestParseMsbConfigMounts(t *testing.T) {
	jsonPayload := `{
		"name": "construct-cli-daemon",
		"mounts": [
			{"type": "Tmpfs", "guest": "/tmp", "size_mib": 512},
			{"type": "Bind", "host": "/host/home", "guest": "/home/construct"},
			{"type": "Bind", "host": "/host/workspaces/proj", "guest": "/workspaces/proj"}
		]
	}`

	mounts := parseMsbConfigMounts(jsonPayload)
	if len(mounts) != 2 {
		t.Fatalf("expected 2 bind mounts, got %d: %+v", len(mounts), mounts)
	}
	if mounts["/home/construct"] != "/host/home" {
		t.Errorf("home mount = %q, want /host/home", mounts["/home/construct"])
	}
	if mounts["/workspaces/proj"] != "/host/workspaces/proj" {
		t.Errorf("project mount = %q, want /host/workspaces/proj", mounts["/workspaces/proj"])
	}
	if _, ok := mounts["/tmp"]; ok {
		t.Errorf("tmpfs mount should not be in bind mounts map")
	}

	// Empty / invalid JSON
	if m := parseMsbConfigMounts(""); len(m) != 0 {
		t.Errorf("expected empty map for empty string, got %+v", m)
	}
	if m := parseMsbConfigMounts("invalid json"); len(m) != 0 {
		t.Errorf("expected empty map for invalid json, got %+v", m)
	}
}
