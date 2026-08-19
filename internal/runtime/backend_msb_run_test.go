package runtime

import (
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/EstebanForge/construct-cli/internal/config"
)

func TestBuildMsbRunSpecMounts(t *testing.T) {
	cfg := config.DefaultConfig()
	spec := BuildMsbRunSpec(&cfg, "sb-test", "/tmp/proj", []int{18080})
	if spec.Name != "sb-test" {
		t.Errorf("Name = %q", spec.Name)
	}
	if spec.Image != "construct-box:latest" {
		t.Errorf("Image = %q", spec.Image)
	}
	if spec.Mounts["/home/linuxbrew/.linuxbrew"].Named != msbVolumePackages {
		t.Errorf("packages mount missing: %+v", spec.Mounts["/home/linuxbrew/.linuxbrew"])
	}
	if spec.Mounts["/home/construct"].Named != msbVolumeHome {
		t.Errorf("home mount missing: %+v", spec.Mounts["/home/construct"])
	}
	if spec.Mounts["/workspace"].Bind != "/tmp/proj" {
		t.Errorf("project mount missing: %+v", spec.Mounts["/workspace"])
	}
	if spec.HostAliasEnv != msbHostAlias || spec.Env["CONSTRUCT_HOST_ALIAS"] != msbHostAlias {
		t.Errorf("host alias env missing: %+v", spec.Env)
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
