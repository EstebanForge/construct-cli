package security

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// RedactionRange represents a byte range that was redacted.
type RedactionRange struct {
	Offset int64
	Length int64
}

// FileRedaction describes redaction applied to a single file.
type FileRedaction struct {
	Path           string
	SourceHash     string
	RedactedHash   string
	RedactedRanges []RedactionRange
	SecretsCount   int
}

// Redactor performs secret redaction on file content.
type Redactor struct {
	marker *Masker
}

// NewRedactor creates a new redactor.
func NewRedactor(maskStyle string) *Redactor {
	return &Redactor{
		marker: NewMasker(maskStyle),
	}
}

// RedactFile redacts secrets from a file based on its type.
func (r *Redactor) RedactFile(path string, content []byte) ([]byte, *FileRedaction, error) {
	redaction := &FileRedaction{
		Path:           path,
		SourceHash:     hashContent(content),
		RedactedRanges: []RedactionRange{},
	}

	ext := strings.TrimPrefix(filepath.Ext(path), ".")

	var redacted []byte
	var err error

	switch ext {
	case "env", "dotenv":
		redacted, redaction.SecretsCount = r.redactDotenv(content)
	case "json":
		redacted, redaction.SecretsCount = r.redactJSON(content)
	case "yaml", "yml":
		redacted, redaction.SecretsCount = r.redactYAML(content)
	case "toml":
		redacted, redaction.SecretsCount = r.redactTOML(content)
	case "ini":
		redacted, redaction.SecretsCount = r.redactINI(content)
	case "properties":
		redacted, redaction.SecretsCount = r.redactProperties(content)
	case "pem", "key", "crt", "cert":
		redacted, redaction.SecretsCount = r.redactPEM(content)
	default:
		// Unknown file type - do content-based scanning only
		redacted, redaction.SecretsCount, err = r.redactGeneric(content)
	}

	if err != nil {
		return nil, nil, err
	}

	redaction.RedactedHash = hashContent(redacted)
	return redacted, redaction, nil
}

// redactDotenv redacts secrets from dotenv files.
func (r *Redactor) redactDotenv(content []byte) ([]byte, int) {
	lines := bytes.Split(content, []byte{'\n'})
	count := 0

	for i, line := range lines {
		// Skip comments and empty lines
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 || trimmed[0] == '#' {
			continue
		}

		// Check if line contains KEY=VALUE
		if idx := bytes.Index(line, []byte{'='}); idx > 0 {
			key := string(bytes.TrimSpace(line[:idx]))
			value := string(line[idx+1:])

			// Check if key indicates a secret
			if r.isSecretKey(key) {
				masked := r.marker.Mask(value)
				lines[i] = []byte(fmt.Sprintf("%s=%s", key, masked))
				count++
			}
		}
	}

	return bytes.Join(lines, []byte{'\n'}), count
}

// redactJSON redacts secrets from JSON files.
func (r *Redactor) redactJSON(content []byte) ([]byte, int) {
	// Simple JSON redaction based on key patterns
	count := 0
	lines := bytes.Split(content, []byte{'\n'})

	for i, line := range lines {
		// Look for "key": "value" patterns
		re := regexp.MustCompile(`"([^"]+)":\s*"([^"]*)"`)
		matches := re.FindAllSubmatchIndex(line, -1)

		for _, match := range matches {
			key := string(line[match[2]:match[3]])
			if r.isSecretKey(key) {
				value := string(line[match[4]:match[5]])
				masked := r.marker.Mask(value)
				replacement := fmt.Sprintf("\"%s\": \"%s\"", key, masked)
				lines[i] = bytes.Replace(line, line[match[0]:match[1]], []byte(replacement), 1)
				count++
			}
		}
	}

	return bytes.Join(lines, []byte{'\n'}), count
}

// redactYAML redacts secrets from YAML files.
func (r *Redactor) redactYAML(content []byte) ([]byte, int) {
	// Simple YAML redaction based on key: value patterns
	count := 0
	lines := bytes.Split(content, []byte{'\n'})

	for i, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 || trimmed[0] == '#' {
			continue
		}

		// Look for key: value patterns
		re := regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_]*):\s*(.+)$`)
		if match := re.FindSubmatch(trimmed); match != nil {
			key := string(match[1])
			value := strings.TrimSpace(string(match[2]))

			// Skip if value looks like another YAML structure
			if strings.HasPrefix(value, "#") || strings.HasPrefix(value, "|") || strings.HasPrefix(value, ">") {
				continue
			}

			if r.isSecretKey(key) && !strings.HasPrefix(value, "[") && !strings.HasPrefix(value, "{") {
				masked := r.marker.Mask(value)
				lines[i] = bytes.Replace(line, match[2], []byte(masked), 1)
				count++
			}
		}
	}

	return bytes.Join(lines, []byte{'\n'}), count
}

// redactTOML redacts secrets from TOML files.
func (r *Redactor) redactTOML(content []byte) ([]byte, int) {
	// Simple TOML redaction (key = "value")
	count := 0
	lines := bytes.Split(content, []byte{'\n'})

	for i, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 || trimmed[0] == '#' {
			continue
		}

		// Look for key = "value" patterns
		re := regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_]*)\s*=\s*"([^"]*)"`)
		if match := re.FindSubmatch(trimmed); match != nil {
			key := string(match[1])
			value := string(match[2])

			if r.isSecretKey(key) {
				masked := r.marker.Mask(value)
				replacement := fmt.Sprintf(`%s = "%s"`, key, masked)
				lines[i] = []byte(replacement)
				count++
			}
		}
	}

	return bytes.Join(lines, []byte{'\n'}), count
}

// redactINI redacts secrets from INI files.
func (r *Redactor) redactINI(content []byte) ([]byte, int) {
	// INI files use key = value or key: value
	count := 0
	lines := bytes.Split(content, []byte{'\n'})

	for i, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 || trimmed[0] == ';' || trimmed[0] == '#' || trimmed[0] == '[' {
			continue
		}

		// Look for key = value or key: value
		re := regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_]*)\s*[=:]\s*(.+)$`)
		if match := re.FindSubmatch(trimmed); match != nil {
			key := string(match[1])
			value := strings.TrimSpace(string(match[2]))

			if r.isSecretKey(key) {
				masked := r.marker.Mask(value)
				replacement := fmt.Sprintf("%s = %s", key, masked)
				lines[i] = []byte(replacement)
				count++
			}
		}
	}

	return bytes.Join(lines, []byte{'\n'}), count
}

// redactProperties redacts secrets from Java properties files.
func (r *Redactor) redactProperties(content []byte) ([]byte, int) {
	// Properties files use key=value or key: value
	return r.redactINI(content)
}

// redactPEM redacts secrets from PEM files (certificates, keys).
func (r *Redactor) redactPEM(content []byte) ([]byte, int) {
	// For PEM files, redact the body between headers
	scanner := bufio.NewScanner(bytes.NewReader(content))
	var output []byte
	count := 0
	inBlock := false

	for scanner.Scan() {
		line := scanner.Bytes()

		if bytes.HasPrefix(line, []byte("-----BEGIN")) {
			inBlock = true
			output = append(output, line...)
			output = append(output, '\n')
			continue
		}

		if bytes.HasPrefix(line, []byte("-----END")) {
			inBlock = false
			output = append(output, line...)
			output = append(output, '\n')
			count++
			continue
		}

		if inBlock {
			// Redact base64-encoded body
			masked := r.marker.Mask(string(line))
			output = append(output, []byte(masked)...)
			output = append(output, '\n')
		} else {
			output = append(output, line...)
			output = append(output, '\n')
		}
	}

	return output, count
}

// redactGeneric performs content-based redaction for unknown file types.
func (r *Redactor) redactGeneric(content []byte) ([]byte, int, error) {
	// For V1, this is a placeholder - we'd integrate with ripgrep for pattern matching
	// For now, return content unchanged
	return content, 0, nil
}

// isSecretKey returns true if a key name suggests it contains a secret.
func (r *Redactor) isSecretKey(key string) bool {
	keyLower := strings.ToLower(key)

	secretIndicators := []string{
		"api_key", "apikey", "api-key",
		"secret", "password", "passwd", "pwd",
		"token", "auth", "authorization",
		"dsn", "connection_string", "connection-string",
		"private_key", "private-key", "privatekey",
		"credential", "cert", "certificate",
	}

	for _, indicator := range secretIndicators {
		if strings.Contains(keyLower, indicator) {
			return true
		}
	}

	return false
}

// hashContent computes a SHA256 hash of content.
func hashContent(content []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(content))
}
