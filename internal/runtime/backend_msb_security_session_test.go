package runtime

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/EstebanForge/construct-cli/internal/config"
)

func TestMsbDaemonSecuritySessionsReuse(t *testing.T) {
	withRootsTestHome(t)
	base := SecuritySessionsBaseDir()
	if base == "" {
		t.Fatal("SecuritySessionsBaseDir returned empty")
	}

	session1 := filepath.Join(base, "session-1", "merged")
	session2 := filepath.Join(base, "session-2", "merged")

	cfg := config.DefaultConfig()
	cfg.Sandbox.MountSkills = false

	// Build spec as if run 1 executed with session1
	spec := BuildMsbRunSpec(&cfg, msbDaemonName, session1, nil, "")

	// 1. BuildMsbRunSpec must collapse the construct.project_dir label to the static base
	if spec.Labels["construct.project_dir"] != base {
		t.Errorf("BuildMsbRunSpec construct.project_dir label = %q, want base %q", spec.Labels["construct.project_dir"], base)
	}

	// 2. The daemon sandbox has mounted the base dir
	dest := GetMsbWorkspaceMountDest(base)
	cfgJSON := fmt.Sprintf(`{"mounts":[{"type":"Bind","host":%q,"guest":%q}]}`, base, dest)

	// 3. Run 2 arrives with session2. It must reuse the daemon without recreation.
	recreate, reason := msbDaemonNeedsRecreate(DaemonMounts{}, spec.Labels, cfgJSON, session2, false, &cfg, msbCachedImageDigest())
	if recreate {
		t.Fatalf("msbDaemonNeedsRecreate returned true (reason %q); ephemeral sessions must reuse the daemon", reason)
	}

	// 4. Legacy compatibility: a running daemon from 3.0.0 whose label had the uncollapsed session1 path
	legacyLabels := map[string]string{}
	for k, v := range spec.Labels {
		legacyLabels[k] = v
	}
	legacyLabels["construct.project_dir"] = session1
	recreateLegacy, reasonLegacy := msbDaemonNeedsRecreate(DaemonMounts{}, legacyLabels, cfgJSON, session2, false, &cfg, msbCachedImageDigest())
	if recreateLegacy {
		t.Fatalf("msbDaemonNeedsRecreate returned true for legacy uncollapsed label (reason %q)", reasonLegacy)
	}
}
