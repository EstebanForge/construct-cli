package config

import (
	"os"
	"path/filepath"
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

[pip]
packages = ["black"]

[tools]
phpbrew = true
nix = true
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
	if config.Tools.PhpBrew != true || config.Tools.Vmr != false || config.Tools.Nix != true {
		t.Errorf("Tools parsing failed")
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
