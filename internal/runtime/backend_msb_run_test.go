package runtime

import (
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
	if spec.Mounts["/workspace"].Bind != "/tmp/proj" {
		t.Errorf("project mount missing: %+v", spec.Mounts["/workspace"])
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
	mounts := msbSandboxMounts(proj)
	paths := MsbPathMaps(proj)
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
	found := false
	for _, pm := range paths {
		if pm.Guest == "/workspace" {
			found = true
		}
	}
	if !found {
		t.Error("MsbPathMaps missing /workspace project translation")
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
