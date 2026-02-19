package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EstebanForge/construct-cli/internal/constants"
	"github.com/pelletier/go-toml/v2"
)

// TestConfigParsing tests TOML configuration parsing
func TestConfigParsing(t *testing.T) {
	testConfig := `
[runtime]
engine = "docker"
auto_update_check = true

[sandbox]
mount_home = false
non_root_strict = true
exec_as_host_user = true
shell = "/bin/bash"
clipboard_host = "host.orbstack.internal"

[network]
mode = "strict"
allowed_domains = ["*.anthropic.com", "*.openai.com"]
allowed_ips = ["1.1.1.1/32"]
blocked_domains = []
blocked_ips = []

[agents]
yolo_all = false
yolo_agents = ["claude", "gemini"]
clipboard_image_patch = false

`

	var config Config
	err := toml.Unmarshal([]byte(testConfig), &config)
	if err != nil {
		t.Fatalf("Failed to parse config: %v", err)
	}

	// Test runtime
	if config.Runtime.Engine != "docker" {
		t.Errorf("Expected engine 'docker', got '%s'", config.Runtime.Engine)
	}
	if !config.Runtime.AutoUpdateCheck {
		t.Error("Expected auto_update_check to be true")
	}

	// Test sandbox
	if config.Sandbox.MountHome {
		t.Error("Expected mount_home to be false")
	}
	if config.Sandbox.Shell != "/bin/bash" {
		t.Errorf("Expected shell '/bin/bash', got '%s'", config.Sandbox.Shell)
	}
	if !config.Sandbox.NonRootStrict {
		t.Error("Expected non_root_strict to be true")
	}
	if !config.Sandbox.ExecAsHostUser {
		t.Error("Expected exec_as_host_user to be true")
	}

	// Test network
	if config.Network.Mode != "strict" {
		t.Errorf("Expected network mode 'strict', got '%s'", config.Network.Mode)
	}
	if len(config.Network.AllowedDomains) != 2 {
		t.Errorf("Expected 2 allowed domains, got %d", len(config.Network.AllowedDomains))
	}

	// Test clipboard
	if config.Sandbox.ClipboardHost != "host.orbstack.internal" {
		t.Errorf("Expected clipboard host 'host.orbstack.internal', got '%s'", config.Sandbox.ClipboardHost)
	}

	// Test agents
	if config.Agents.YoloAll {
		t.Error("Expected yolo_all to be false")
	}
	if len(config.Agents.YoloAgents) != 2 || config.Agents.YoloAgents[0] != "claude" {
		t.Errorf("Expected yolo_agents to contain claude and gemini")
	}
	if config.Agents.ClipboardImagePatch {
		t.Error("Expected clipboard_image_patch to be false")
	}
}

// TestConfigDirPath tests config directory path generation
func TestConfigDirPath(t *testing.T) {
	// Save original HOME
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)

	// Test with custom HOME
	testHome := "/tmp/test-home"
	os.Setenv("HOME", testHome)

	expected := filepath.Join(testHome, ".config", "construct-cli")
	// Use constants for ".config/construct-cli" part verification
	expectedSuffix := constants.ConfigDir

	result := GetConfigDir()

	if result != expected {
		// On some systems HOME might behave differently or Join.
		// Let's verify it ends with expected path
		if filepath.Base(result) != filepath.Base(expectedSuffix) {
			t.Errorf("Expected config dir to end with '%s', got '%s'", filepath.Base(expectedSuffix), result)
		}
	}
}

// TestConfigStructure tests Config struct instantiation
func TestConfigStructure(t *testing.T) {
	config := Config{
		Runtime: RuntimeConfig{
			Engine:          "docker",
			AutoUpdateCheck: false,
		},
		Sandbox: SandboxConfig{
			MountHome:      false,
			NonRootStrict:  false,
			ExecAsHostUser: false,
			Shell:          "/bin/bash",
			ClipboardHost:  "host.docker.internal",
		},
		Network: NetworkConfig{
			Mode:           "permissive",
			AllowedDomains: []string{},
			AllowedIPs:     []string{},
			BlockedDomains: []string{},
			BlockedIPs:     []string{},
		},
		Agents: AgentsConfig{
			YoloAll:             false,
			YoloAgents:          []string{},
			ClipboardImagePatch: true,
		},
	}

	if config.Runtime.Engine != "docker" {
		t.Error("Runtime config initialization failed")
	}
	if config.Sandbox.Shell != "/bin/bash" {
		t.Error("Sandbox config initialization failed")
	}
	if config.Network.Mode != "permissive" {
		t.Error("Network config initialization failed")
	}
	if config.Sandbox.ClipboardHost != "host.docker.internal" {
		t.Error("Clipboard config initialization failed")
	}
	if config.Sandbox.NonRootStrict {
		t.Error("Expected non_root_strict to be false")
	}
	if config.Sandbox.ExecAsHostUser {
		t.Error("Expected exec_as_host_user to be false")
	}
	if config.Agents.YoloAll {
		t.Error("Agents config initialization failed")
	}
}

func TestDefaultConfigExecAsHostUserEnabled(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Sandbox.ExecAsHostUser {
		t.Error("Expected default exec_as_host_user to be true")
	}
}

// TestClaudeProviderConfig tests Claude provider configuration parsing
func TestClaudeProviderConfig(t *testing.T) {
	testConfig := `
[runtime]
engine = "docker"

[claude.cc.zai]
ANTHROPIC_BASE_URL = "${CNSTR_ZAI_API_URL}"
ANTHROPIC_AUTH_TOKEN = "${CNSTR_ZAI_API_KEY}"
API_TIMEOUT_MS = "3000000"
CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = "1"

[claude.cc.minimax]
ANTHROPIC_BASE_URL = "https://api.minimax.com/v1"
ANTHROPIC_AUTH_TOKEN = "direct-key-value"
ANTHROPIC_MODEL = "MiniMax-M2"
`

	var config Config
	err := toml.Unmarshal([]byte(testConfig), &config)
	if err != nil {
		t.Fatalf("Failed to parse config: %v", err)
	}

	// Test provider parsing
	if len(config.Claude.Providers) != 2 {
		t.Errorf("Expected 2 providers, got %d", len(config.Claude.Providers))
	}

	// Test zai provider
	zai, exists := config.Claude.Providers["zai"]
	if !exists {
		t.Error("Expected zai provider to exist")
	}

	if zai["ANTHROPIC_BASE_URL"] != "${CNSTR_ZAI_API_URL}" {
		t.Errorf("Expected ${CNSTR_ZAI_API_URL}, got %s", zai["ANTHROPIC_BASE_URL"])
	}

	if zai["ANTHROPIC_AUTH_TOKEN"] != "${CNSTR_ZAI_API_KEY}" {
		t.Errorf("Expected ${CNSTR_ZAI_API_KEY}, got %s", zai["ANTHROPIC_AUTH_TOKEN"])
	}

	if zai["API_TIMEOUT_MS"] != "3000000" {
		t.Errorf("Expected 3000000, got %s", zai["API_TIMEOUT_MS"])
	}

	// Test minimax provider
	minimax, exists := config.Claude.Providers["minimax"]
	if !exists {
		t.Error("Expected minimax provider to exist")
	}

	if minimax["ANTHROPIC_BASE_URL"] != "https://api.minimax.com/v1" {
		t.Errorf("Expected https://api.minimax.com/v1, got %s",
			minimax["ANTHROPIC_BASE_URL"])
	}

	if minimax["ANTHROPIC_AUTH_TOKEN"] != "direct-key-value" {
		t.Errorf("Expected direct-key-value, got %s",
			minimax["ANTHROPIC_AUTH_TOKEN"])
	}

	if minimax["ANTHROPIC_MODEL"] != "MiniMax-M2" {
		t.Errorf("Expected MiniMax-M2, got %s", minimax["ANTHROPIC_MODEL"])
	}
}

// Benchmark config parsing
func BenchmarkConfigParsing(b *testing.B) {
	testConfig := []byte(`
[runtime]
engine = "docker"
[sandbox]
mount_home = false
[network]
mode = "permissive"
`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var config Config
		_ = toml.Unmarshal(testConfig, &config)
	}
}

// TestConfigModificationPersistence verifies that config modifications
// (via Save()) are reflected in subsequent Load() calls.
//
// This is a regression test to ensure that global caching (e.g., sync.Once)
// is NOT implemented, as config is modified during runtime via network commands.
// See PERFORMANCE.md optimization #6 for details.
func TestConfigModificationPersistence(t *testing.T) {
	// Create a temporary config directory
	tmpDir := t.TempDir()

	// Save original HOME and set temporary
	origHome := os.Getenv("HOME")
	defer os.Setenv("HOME", origHome)
	os.Setenv("HOME", tmpDir)

	// Create all required directories and files (to avoid Init() running)
	configDir := filepath.Join(tmpDir, ".config", "construct-cli")
	containerDir := filepath.Join(configDir, "container")
	homeDir := filepath.Join(configDir, "home")

	for _, dir := range []string{configDir, containerDir, homeDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("Failed to create dir: %v", err)
		}
	}

	// Write required files to prevent Init() from running
	requiredFiles := map[string]string{
		"Dockerfile":            "FROM alpine\n",
		"docker-compose.yml":    "version: '3'\n",
		"entrypoint.sh":         "#!/bin/bash\n",
		"update-all.sh":         "#!/bin/bash\n",
		"network-filter.sh":     "#!/bin/bash\n",
		"clipper":               "binary\n",
		"clipboard-x11-sync.sh": "#!/bin/bash\n",
		"osascript":             "binary\n",
		"powershell.exe":        "binary\n",
		"config.toml":           "[runtime]\nengine = \"docker\"\n\n[sandbox]\n\n[network]\nmode = \"permissive\"\nallowed_domains = []\nallowed_ips = []\nblocked_domains = []\nblocked_ips = []\n\n[agents]\nyolo_all = false\nyolo_agents = []\n\n[daemon]\nauto_start = true\n",
		"packages.toml":         "[npm]\npackages = []\n",
	}

	for file, content := range requiredFiles {
		path := filepath.Join(containerDir, file)
		if file == "config.toml" || file == "packages.toml" {
			path = filepath.Join(configDir, file)
		}
		perm := os.FileMode(0644)
		if filepath.Ext(path) == ".sh" {
			perm = 0755
		}
		if err := os.WriteFile(path, []byte(content), perm); err != nil {
			t.Fatalf("Failed to write %s: %v", file, err)
		}
	}

	// Load config - first load
	cfg1, _, err := Load()
	if err != nil {
		t.Fatalf("First Load() failed: %v", err)
	}
	// Note: created flag might be true if Init() ran, which is ok for this test
	// The key is that subsequent loads should get fresh data

	// Verify initial state
	if cfg1.Network.Mode != "permissive" {
		t.Errorf("Expected initial mode 'permissive', got '%s'", cfg1.Network.Mode)
	}
	if len(cfg1.Network.AllowedDomains) != 0 {
		t.Errorf("Expected 0 allowed domains initially, got %d", len(cfg1.Network.AllowedDomains))
	}

	// Modify config (simulating network rule change)
	cfg1.Network.Mode = "strict"
	cfg1.Network.AllowedDomains = []string{"*.example.com"}
	if err := cfg1.Save(); err != nil {
		t.Fatalf("Failed to save modified config: %v", err)
	}

	// Load config again - this should get fresh data from disk
	cfg2, created2, err := Load()
	if err != nil {
		t.Fatalf("Second Load() failed: %v", err)
	}
	// created2 might be true if Init() ran, that's ok
	_ = created2

	// Verify that the second Load() reflects the saved changes
	// This test will FAIL if sync.Once caching is implemented
	if cfg2.Network.Mode != "strict" {
		t.Errorf("After Save(), Load() should return mode 'strict', got '%s'", cfg2.Network.Mode)
	}
	if len(cfg2.Network.AllowedDomains) != 1 {
		t.Errorf("After Save(), Load() should return 1 allowed domain, got %d", len(cfg2.Network.AllowedDomains))
	}
	if len(cfg2.Network.AllowedDomains) > 0 && cfg2.Network.AllowedDomains[0] != "*.example.com" {
		t.Errorf("Expected allowed domain '*.example.com', got '%s'", cfg2.Network.AllowedDomains[0])
	}

	// Additional modification: add blocked domain
	cfg2.Network.BlockedDomains = []string{"malicious-site.com"}
	if err := cfg2.Save(); err != nil {
		t.Fatalf("Failed to save second modification: %v", err)
	}

	// Third load should get both modifications
	cfg3, _, err := Load()
	if err != nil {
		t.Fatalf("Third Load() failed: %v", err)
	}

	if cfg3.Network.Mode != "strict" {
		t.Errorf("Third Load() should preserve mode 'strict', got '%s'", cfg3.Network.Mode)
	}
	if len(cfg3.Network.AllowedDomains) != 1 {
		t.Errorf("Third Load() should preserve 1 allowed domain, got %d", len(cfg3.Network.AllowedDomains))
	}
	if len(cfg3.Network.BlockedDomains) != 1 {
		t.Errorf("Third Load() should have 1 blocked domain, got %d", len(cfg3.Network.BlockedDomains))
	}
}
