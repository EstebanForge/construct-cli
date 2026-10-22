package runtime

import (
	"testing"
)

// TestDaemonSpecEnvHashRoundTrip pins the env-hash label across the spec
// persistence path. The daemon decision prefers the spec file over live
// record labels, so a label dropped by specFromLabels/toLabels recreates
// the daemon on EVERY boot after the first (the matrix caught this live:
// outcome=recreate on a warm stop/start with reason "[network] mode or
// sandbox env changed").
func TestDaemonSpecEnvHashRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	labels := map[string]string{
		"construct.project_dir":   "/proj",
		DaemonMountsLabelKey:      "mounts",
		DaemonSkillsLabelKey:      "skills",
		DaemonSudoLabelKey:        "free",
		DaemonImageDigestLabelKey: "sha256:digest",
		DaemonSDKVersionLabelKey:  "0.7.8",
		DaemonEnvHashLabelKey:     "abc12345",
	}

	spec := specFromLabels(labels)
	if spec.EnvHash != "abc12345" {
		t.Fatalf("specFromLabels dropped env_hash: %+v", spec)
	}
	got := spec.toLabels()
	if got[DaemonEnvHashLabelKey] != "abc12345" {
		t.Fatalf("toLabels dropped env_hash: %+v", got)
	}

	// The persisted file must carry it too: save from a run spec's labels,
	// load back, and confirm the decision labels see the hash.
	if err := saveMsbDaemonSpecFromRunSpec(&MsbRunSpec{Labels: labels}); err != nil {
		t.Fatal(err)
	}
	decision := msbDaemonDecisionLabels(nil)
	if decision[DaemonEnvHashLabelKey] != "abc12345" {
		t.Fatalf("decision labels lost env_hash across the spec file: %+v", decision)
	}
}
