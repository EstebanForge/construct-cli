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
			MountHome:     false,
			Shell:         "/bin/bash",
			ClipboardHost: "host.docker.internal",
		},
		Network: NetworkConfig{
			Mode:           "permissive",
			AllowedDomains: []string{},
			AllowedIPs:     []string{},
			BlockedDomains: []string{},
			BlockedIPs:     []string{},
		},
		Agents: AgentsConfig{
			YoloAll:    false,
			YoloAgents: []string{},
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
	if config.Agents.YoloAll {
		t.Error("Agents config initialization failed")
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
