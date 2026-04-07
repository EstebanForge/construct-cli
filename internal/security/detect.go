package security

import (
	"path/filepath"
	"strings"
)

// SecretPattern is a regex pattern that may indicate a secret.
type SecretPattern struct {
	Name    string
	Pattern string
}

// DefaultSecretPatterns returns baseline secret detection patterns.
func DefaultSecretPatterns() []SecretPattern {
	return []SecretPattern{
		// Private key blocks
		{Name: "private_key_block", Pattern: `-----BEGIN [A-Z]+ PRIVATE KEY-----`},
		// Generic secret indicators
		{Name: "api_key", Pattern: `(?:api[_-]?key|apikey)['"\s]*[:=]['"\s]*[a-zA-Z0-9_\-]{16,}`},
		{Name: "secret_token", Pattern: `(?:secret[_-]?token|secret[_-]?key)['"\s]*[:=]['"\s]*[a-zA-Z0-9_\-]{16,}`},
		{Name: "password", Pattern: `(?:password|passwd|pwd)['"\s]*[:=]['"\s]*[^\s]{8,}`},
		{Name: "auth_token", Pattern: `(?:auth[_-]?token|auth[_-]?key)['"\s]*[:=]['"\s]*[a-zA-Z0-9_\-]{16,}`},
		{Name: "dsn", Pattern: `(?:dsn|connection[_-]?string)['"\s]*[:=]['"\s]*[^\s]{20,}`},
		// Provider-specific patterns
		{Name: "github_token", Pattern: `ghp_[a-zA-Z0-9]{36}`},
		{Name: "openai_key", Pattern: `sk-[a-zA-Z0-9]{48}`},
		{Name: "anthropic_key", Pattern: `sk-ant-[a-zA-Z0-9\-]{95}`},
		{Name: "aws_key_id", Pattern: `AKIA[0-9A-Z]{16}`},
		{Name: "stripe_key", Pattern: `sk_(live|test)_[a-zA-Z0-9]{24}`},
		{Name: "slack_token", Pattern: `xox[baprs]-[a-zA-Z0-9\-]{10,}`},
	}
}

// CandidateFiles returns path globs that are high-probability secret files.
func CandidateFiles() []string {
	return []string{
		"*.env",
		".env.*",
		"*.pem",
		"*.key",
		"*.p12",
		"*.pfx",
		"id_rsa",
		"id_ed25519",
		".npmrc",
		".pypirc",
		".netrc",
		".dockercfg",
		"docker-compose*.yml",
		"docker-compose*.yaml",
		"*.tfvars",
		"*.tfvars.json",
		"application*.yml",
		"application*.yaml",
		"application*.properties",
		"config*.json",
		"config*.yaml",
		"config*.yml",
		"config*.toml",
		"*.ini",
	}
}

// HardExcludedPaths returns paths that are never scanned.
func HardExcludedPaths() []string {
	return []string{
		".git",
		"node_modules",
		".venv",
		"venv",
		"vendor",
		"target",
		"dist",
		"build",
		".next",
		".nuxt",
		".turbo",
		".cache",
	}
}

// IsHardExcluded returns true if the path should never be scanned.
func IsHardExcluded(path string, projectRoot string) bool {
	relPath, err := filepath.Rel(projectRoot, path)
	if err != nil {
		return false
	}

	components := strings.Split(filepath.ToSlash(relPath), "/")
	for _, component := range components {
		for _, excluded := range HardExcludedPaths() {
			if component == excluded {
				return true
			}
		}
	}
	return false
}

// MatchesCandidatePattern returns true if the file matches any candidate glob pattern.
func MatchesCandidatePattern(relPath string, extraDenyPaths []string) bool {
	baseName := filepath.Base(relPath)

	// Check default candidates
	for _, pattern := range CandidateFiles() {
		matched, err := filepath.Match(pattern, baseName)
		if err == nil && matched {
			return true
		}
		// Also check full path for patterns like .env.*
		matched, err = filepath.Match(pattern, relPath)
		if err == nil && matched {
			return true
		}
	}

	// Check user-specified deny paths
	for _, pattern := range extraDenyPaths {
		matched, err := filepath.Match(pattern, relPath)
		if err == nil && matched {
			return true
		}
	}

	return false
}
