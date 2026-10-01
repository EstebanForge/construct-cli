package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/EstebanForge/construct-cli/internal/config"
)

// The msb daemon recreate decision reads drift inputs (project dir, mount
// hash, skills hash, sudo policy, image digest) from the sandbox record's
// labels. The snapshot-fork recreate boots the replacement daemon from a
// disk snapshot, and msb restore does not carry labels ("restore configures
// restoration, not ... startup-command selection" — labels included), so a
// forked daemon would read as maximally drifted and recreate again on the
// very next run. This spec file is the construct-owned home for those
// inputs: written on every cold create and fork, read preferentially by the
// decision, and migrated once from record labels for daemons that predate
// it. msb labels stay the fallback when the file is absent.

const msbDaemonSpecVersion = 1

// msbDaemonSpec mirrors the construct.* label set BuildMsbRunSpec stamps on
// a daemon sandbox. Field-for-field with the labels so the decision engine
// cannot tell the two sources apart.
type msbDaemonSpec struct {
	Version     int    `json:"version"`
	ProjectDir  string `json:"project_dir,omitempty"`
	MountsHash  string `json:"mounts_hash,omitempty"`
	SkillsHash  string `json:"skills_hash,omitempty"`
	Sudo        string `json:"sudo,omitempty"`
	ImageDigest string `json:"image_digest,omitempty"`
	SDKVersion  string `json:"sdk_version,omitempty"`
}

func msbDaemonSpecPath() string {
	return filepath.Join(config.GetConfigDir(), "msb-daemon-spec.json")
}

// loadMsbDaemonSpec returns the stored spec; ok=false when absent. A corrupt
// file surfaces as an error so the caller can fall back to record labels
// instead of silently treating a torn write as "no drift".
func loadMsbDaemonSpec() (*msbDaemonSpec, bool, error) {
	data, err := os.ReadFile(msbDaemonSpecPath())
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var spec msbDaemonSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, false, fmt.Errorf("parse daemon spec: %w", err)
	}
	return &spec, true, nil
}

func saveMsbDaemonSpec(spec *msbDaemonSpec) error {
	if spec == nil {
		return nil
	}
	spec.Version = msbDaemonSpecVersion
	path := msbDaemonSpecPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// specFromLabels mirrors a sandbox record's construct.* labels into a spec.
func specFromLabels(labels map[string]string) *msbDaemonSpec {
	return &msbDaemonSpec{
		ProjectDir:  labels["construct.project_dir"],
		MountsHash:  labels[DaemonMountsLabelKey],
		SkillsHash:  labels[DaemonSkillsLabelKey],
		Sudo:        labels[DaemonSudoLabelKey],
		ImageDigest: labels[DaemonImageDigestLabelKey],
		SDKVersion:  labels[DaemonSDKVersionLabelKey],
	}
}

// toLabels renders the spec as the label map the decision engine consumes.
func (s *msbDaemonSpec) toLabels() map[string]string {
	labels := map[string]string{}
	if s.ProjectDir != "" {
		labels["construct.project_dir"] = s.ProjectDir
	}
	if s.MountsHash != "" {
		labels[DaemonMountsLabelKey] = s.MountsHash
	}
	if s.SkillsHash != "" {
		labels[DaemonSkillsLabelKey] = s.SkillsHash
	}
	if s.Sudo != "" {
		labels[DaemonSudoLabelKey] = s.Sudo
	}
	if s.ImageDigest != "" {
		labels[DaemonImageDigestLabelKey] = s.ImageDigest
	}
	if s.SDKVersion != "" {
		labels[DaemonSDKVersionLabelKey] = s.SDKVersion
	}
	return labels
}

// saveMsbDaemonSpecFromRunSpec records the decision inputs of a daemon
// sandbox from the spec it was (re)created with. Called after a successful
// cold create and after a successful snapshot fork — the fork path has no
// other carrier for these inputs.
func saveMsbDaemonSpecFromRunSpec(spec *MsbRunSpec) error {
	if spec == nil {
		return nil
	}
	return saveMsbDaemonSpec(specFromLabels(spec.Labels))
}

// msbDaemonDecisionLabels returns the drift inputs for the daemon recreate
// decision. The construct-owned spec file wins when present (a forked
// daemon's msb record carries no labels); otherwise the record's own labels
// are used and migrated into the file so the first fork finds them.
func msbDaemonDecisionLabels(sandboxLabels map[string]string) map[string]string {
	if spec, ok, err := loadMsbDaemonSpec(); err == nil && ok {
		return spec.toLabels()
	}
	if len(sandboxLabels) > 0 {
		_ = saveMsbDaemonSpec(specFromLabels(sandboxLabels)) //nolint:errcheck // best-effort migration; record labels remain the fallback
	}
	return sandboxLabels
}
