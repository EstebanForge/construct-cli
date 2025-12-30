package migration

import (
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestMergeTemplateWithBackupSkipsIncompatibleTypes(t *testing.T) {
	template := `
[network]
mode = "permissive"
allowed_domains = ["api.z.ai"]
allowed_ips = ["1.1.1.1/32"]
blocked_domains = ["*.malicious-site.example"]
blocked_ips = ["203.0.113.0/24"]
`

	backup := `
[network]
mode = "strict"
allowed_domains = true
allowed_ips = true
blocked_domains = true
blocked_ips = false
`

	merged, err := mergeTemplateWithBackup([]byte(template), []byte(backup))
	if err != nil {
		t.Fatalf("mergeTemplateWithBackup error: %v", err)
	}

	var cfg map[string]interface{}
	if err := toml.Unmarshal(merged, &cfg); err != nil {
		t.Fatalf("merged config invalid TOML: %v", err)
	}

	network, ok := cfg["network"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected network table, got %T", cfg["network"])
	}

	if mode, ok := network["mode"].(string); !ok || mode != "strict" {
		t.Fatalf("expected mode to be strict, got %#v", network["mode"])
	}

	if _, ok := network["allowed_domains"].([]interface{}); !ok {
		t.Fatalf("expected allowed_domains to be array, got %T", network["allowed_domains"])
	}
	if _, ok := network["allowed_ips"].([]interface{}); !ok {
		t.Fatalf("expected allowed_ips to be array, got %T", network["allowed_ips"])
	}
	if _, ok := network["blocked_domains"].([]interface{}); !ok {
		t.Fatalf("expected blocked_domains to be array, got %T", network["blocked_domains"])
	}
	if _, ok := network["blocked_ips"].([]interface{}); !ok {
		t.Fatalf("expected blocked_ips to be array, got %T", network["blocked_ips"])
	}
}

func TestMergeTemplateWithBackupMissingKeysPreservesUserValues(t *testing.T) {
	template := `
[brew]
taps = ["core"]
packages = ["one", "two"]

[tools]
bun = false
`

	backup := `
[brew]
taps = ["custom"]
packages = ["two"]
`

	merged, err := mergeTemplateWithBackupMissingKeys([]byte(template), []byte(backup))
	if err != nil {
		t.Fatalf("mergeTemplateWithBackupMissingKeys error: %v", err)
	}

	var cfg map[string]interface{}
	if err := toml.Unmarshal(merged, &cfg); err != nil {
		t.Fatalf("merged config invalid TOML: %v", err)
	}

	brew, ok := cfg["brew"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected brew table, got %T", cfg["brew"])
	}

	if taps, ok := brew["taps"].([]interface{}); !ok || len(taps) != 1 || taps[0] != "custom" {
		t.Fatalf("expected taps to be preserved, got %#v", brew["taps"])
	}
	if pkgs, ok := brew["packages"].([]interface{}); !ok || len(pkgs) != 1 || pkgs[0] != "two" {
		t.Fatalf("expected packages to be preserved, got %#v", brew["packages"])
	}

	tools, ok := cfg["tools"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected tools table, got %T", cfg["tools"])
	}
	if bun, ok := tools["bun"].(bool); !ok || bun != false {
		t.Fatalf("expected bun default to be added, got %#v", tools["bun"])
	}
}
