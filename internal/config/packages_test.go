package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestPackagesConfigParsing(t *testing.T) {
	testConfig := `
[apt]
packages = ["htop", "vim"]

[brew]
taps = ["common-family/homebrew-tap"]
packages = ["fastlane"]

[npm]
packages = ["typescript"]

[post_install]
commands = ["agent-browser install --with-deps"]

[pip]
packages = ["black"]

[tools]
phpbrew = true
nix = true
nvm = true
vmr = false
asdf = true
mise = false
`

	var config PackagesConfig
	err := toml.Unmarshal([]byte(testConfig), &config)
	if err != nil {
		t.Fatalf("Failed to parse packages config: %v", err)
	}

	if len(config.Apt.Packages) != 2 || config.Apt.Packages[0] != "htop" {
		t.Errorf("Apt packages parsing failed")
	}
	if len(config.Brew.Taps) != 1 || config.Brew.Taps[0] != "common-family/homebrew-tap" {
		t.Errorf("Brew taps parsing failed")
	}
	if config.Tools.PhpBrew != true || config.Tools.Vmr != false || config.Tools.Nix != true || config.Tools.Nvm != true {
		t.Errorf("Tools parsing failed")
	}
	if len(config.PostInstall.Commands) != 1 || config.PostInstall.Commands[0] != "agent-browser install --with-deps" {
		t.Errorf("Post_install parsing failed")
	}
}

func TestGenerateInstallScriptSudoDetection(t *testing.T) {
	config := &PackagesConfig{}
	script := config.GenerateInstallScript()

	// Verify sudo detection block is present
	if !strings.Contains(script, "SUDO_AVAILABLE=1") {
		t.Error("Script should initialize SUDO_AVAILABLE variable")
	}
	if !strings.Contains(script, "if [ \"$(id -u)\" = \"0\" ]; then") {
		t.Error("Script should check if running as root")
	}
	if !strings.Contains(script, "elif sudo -n true 2>/dev/null; then") {
		t.Error("Script should test if sudo works non-interactively")
	}
	if !strings.Contains(script, "SUDO_AVAILABLE=0") {
		t.Error("Script should set SUDO_AVAILABLE=0 when sudo unavailable")
	}

	// Verify no hardcoded 'sudo apt-get' (should use $SUDO apt-get)
	if strings.Contains(script, "sudo apt-get") {
		t.Error("Script should not contain hardcoded 'sudo apt-get', should use '$SUDO apt-get'")
	}

	// Verify $SUDO variable is used for apt-get commands
	if !strings.Contains(script, "$SUDO apt-get") {
		t.Error("Script should use '$SUDO apt-get' for privileged operations")
	}

	// Verify SUDO_AVAILABLE check wraps critical packages
	if !strings.Contains(script, "if [ \"$SUDO_AVAILABLE\" = \"1\" ]; then") {
		t.Error("Script should check SUDO_AVAILABLE before running privileged operations")
	}
}

func TestGenerateInstallScriptWithAptPackages(t *testing.T) {
	config := &PackagesConfig{
		Apt: AptConfig{
			Packages: []string{"htop", "vim"},
		},
	}
	script := config.GenerateInstallScript()

	// Verify APT packages section uses SUDO_AVAILABLE check
	if !strings.Contains(script, "Installing APT packages") {
		t.Error("Script should contain APT packages section")
	}

	// Verify packages are included
	if !strings.Contains(script, "htop") || !strings.Contains(script, "vim") {
		t.Error("Script should contain specified APT packages")
	}

	// Verify no hardcoded sudo in APT section
	if strings.Contains(script, "sudo apt-get install -y htop") {
		t.Error("APT section should not use hardcoded sudo")
	}
}

func TestGenerateInstallScriptContinuesOnBrewFailures(t *testing.T) {
	config := &PackagesConfig{
		Npm: NpmConfig{
			Packages: []string{"@github/copilot", "cline"},
		},
	}
	script := config.GenerateInstallScript()

	if !strings.Contains(script, "brew install imagemagick || echo") {
		t.Error("Script should guard imagemagick install failures")
	}
	if !strings.Contains(script, "brew install topgrade || echo") {
		t.Error("Script should guard topgrade install failures")
	}
	if !strings.Contains(script, "if command -v brew &> /dev/null; then") {
		t.Error("Script should check for brew before brew installs")
	}

	if !strings.Contains(script, "npm install -g @github/copilot || echo") {
		t.Error("Script should install npm packages individually with failure guards")
	}
	if !strings.Contains(script, "npm install -g cline || echo") {
		t.Error("Script should include all npm packages as separate guarded commands")
	}
}

func TestGenerateInstallScriptIncludesDiagnosticsAndVerification(t *testing.T) {
	config := &PackagesConfig{}
	script := config.GenerateInstallScript()

	if !strings.Contains(script, "=== Construct setup diagnostics ===") {
		t.Error("Script should include setup diagnostics header")
	}
	if !strings.Contains(script, "Homebrew dir not writable by current user") {
		t.Error("Script should include Homebrew writability diagnostics")
	}
	if !strings.Contains(script, "Configuring npm global prefix") {
		t.Error("Script should configure npm prefix before npm installs")
	}
	if !strings.Contains(script, "npm config set prefix \"$HOME/.npm-global\"") {
		t.Error("Script should set npm global prefix to $HOME/.npm-global")
	}
	if !strings.Contains(script, "Post-install command verification") {
		t.Error("Script should include post-install command verification section")
	}
	if !strings.Contains(script, "for cmd in claude amp copilot opencode") {
		t.Error("Script should verify key agent commands after installation")
	}
	if !strings.Contains(script, "If these agents are expected, re-run: construct sys install-packages") {
		t.Error("Script should include guidance when expected commands are missing")
	}
}

func TestLoadPackages(t *testing.T) {
	// Setup temporary home for config
	tmpDir, err := os.MkdirTemp("", "construct-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	configDir := GetConfigDir()
	err = os.MkdirAll(configDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}

	// Test missing file
	config, err := LoadPackages()
	if err != nil {
		t.Errorf("Expected no error for missing file, got %v", err)
	}
	if config == nil {
		t.Error("Expected non-nil config for missing file")
	}

	// Test valid file
	testConfig := `
[apt]
packages = ["htop"]
`
	err = os.WriteFile(filepath.Join(configDir, "packages.toml"), []byte(testConfig), 0644)
	if err != nil {
		t.Fatalf("Failed to write test packages.toml: %v", err)
	}

	config, err = LoadPackages()
	if err != nil {
		t.Errorf("Failed to load packages.toml: %v", err)
	}
	if len(config.Apt.Packages) != 1 || config.Apt.Packages[0] != "htop" {
		t.Errorf("Expected 1 apt package 'htop', got %v", config.Apt.Packages)
	}

	// Test invalid file
	err = os.WriteFile(filepath.Join(configDir, "packages.toml"), []byte("invalid = toml = structure"), 0644)
	if err != nil {
		t.Fatalf("Failed to write invalid packages.toml: %v", err)
	}

	_, err = LoadPackages()
	if err == nil {
		t.Error("Expected error for invalid TOML, got nil")
	}
}
