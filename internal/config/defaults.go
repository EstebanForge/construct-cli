// Package config manages configuration loading and persistence.
package config

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// FindMissingKeys returns the list of default keys not present in config.toml.
func FindMissingKeys(configPath string) ([]string, error) {
	userConfig, err := readTomlMap(configPath)
	if err != nil {
		return nil, err
	}
	defaultConfig, err := defaultConfigMap()
	if err != nil {
		return nil, err
	}

	userFlat := flattenTomlMap(userConfig, "")
	defaultFlat := flattenTomlMap(defaultConfig, "")

	missing := make([]string, 0)
	for key := range defaultFlat {
		if _, ok := userFlat[key]; !ok {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)

	return missing, nil
}

// FixMissingDefaults appends missing default keys into config.toml with a backup.
func FixMissingDefaults(configPath string) (bool, []string, error) {
	userConfig, err := readTomlMap(configPath)
	if err != nil {
		return false, nil, err
	}
	defaultConfig, err := defaultConfigMap()
	if err != nil {
		return false, nil, err
	}

	userFlat := flattenTomlMap(userConfig, "")
	defaultFlat := flattenTomlMap(defaultConfig, "")

	missing := make([]string, 0)
	missingBySection := make(map[string]map[string]interface{})
	for key, value := range defaultFlat {
		if _, ok := userFlat[key]; ok {
			continue
		}
		missing = append(missing, key)
		section, name := splitKeyPath(key)
		if _, ok := missingBySection[section]; !ok {
			missingBySection[section] = make(map[string]interface{})
		}
		missingBySection[section][name] = value
	}
	if len(missing) == 0 {
		return false, nil, nil
	}
	sort.Strings(missing)

	data, err := os.ReadFile(configPath)
	if err != nil {
		return false, nil, err
	}
	updated, err := injectMissingDefaults(string(data), missingBySection)
	if err != nil {
		return false, nil, err
	}

	if err := backupConfig(configPath, data); err != nil {
		return false, nil, err
	}
	if err := os.WriteFile(configPath, []byte(updated), 0644); err != nil {
		return false, nil, err
	}

	return true, missing, nil
}

func readTomlMap(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var config map[string]interface{}
	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return config, nil
}

func defaultConfigMap() (map[string]interface{}, error) {
	data, err := toml.Marshal(DefaultConfig())
	if err != nil {
		return nil, fmt.Errorf("marshal defaults: %w", err)
	}
	var config map[string]interface{}
	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse defaults: %w", err)
	}
	return config, nil
}

func flattenTomlMap(input map[string]interface{}, prefix string) map[string]interface{} {
	flat := make(map[string]interface{})
	for key, value := range input {
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}
		switch typed := value.(type) {
		case map[string]interface{}:
			for k, v := range flattenTomlMap(typed, fullKey) {
				flat[k] = v
			}
		default:
			flat[fullKey] = value
		}
	}
	return flat
}

func splitKeyPath(key string) (string, string) {
	parts := strings.Split(key, ".")
	if len(parts) == 1 {
		return "", parts[0]
	}
	return strings.Join(parts[:len(parts)-1], "."), parts[len(parts)-1]
}

type sectionHeader struct {
	name  string
	index int
}

func injectMissingDefaults(content string, missingBySection map[string]map[string]interface{}) (string, error) {
	lines := strings.Split(content, "\n")
	headers := findSectionHeaders(lines)
	headerIndex := make(map[string]int)
	for _, header := range headers {
		headerIndex[header.name] = header.index
	}

	insertions := make([]struct {
		index int
		lines []string
	}, 0)

	sections := make([]string, 0, len(missingBySection))
	for section := range missingBySection {
		sections = append(sections, section)
	}
	sort.Strings(sections)

	for _, section := range sections {
		keys := make([]string, 0, len(missingBySection[section]))
		for key := range missingBySection[section] {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		block, err := buildMissingBlock(keys, missingBySection[section])
		if err != nil {
			return "", err
		}

		if section == "" {
			lines = append(lines, "")
			lines = append(lines, block...)
			continue
		}

		if idx, ok := headerIndex[section]; ok {
			end := len(lines)
			for _, header := range headers {
				if header.index > idx {
					end = header.index
					break
				}
			}
			insertions = append(insertions, struct {
				index int
				lines []string
			}{index: end, lines: block})
		} else {
			lines = append(lines, "")
			lines = append(lines, fmt.Sprintf("[%s]", section))
			lines = append(lines, block...)
		}
	}

	if len(insertions) > 0 {
		sort.Slice(insertions, func(i, j int) bool {
			return insertions[i].index > insertions[j].index
		})
		for _, insertion := range insertions {
			if insertion.index < 0 || insertion.index > len(lines) {
				return "", fmt.Errorf("invalid insertion index for defaults")
			}
			block := append([]string{""}, insertion.lines...)
			lines = append(lines[:insertion.index], append(block, lines[insertion.index:]...)...)
		}
	}

	result := strings.Join(lines, "\n")
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}

	return result, nil
}

func findSectionHeaders(lines []string) []sectionHeader {
	headers := make([]sectionHeader, 0)
	for i, line := range lines {
		section, ok := parseSectionHeader(line)
		if ok {
			headers = append(headers, sectionHeader{name: section, index: i})
		}
	}
	return headers
}

func parseSectionHeader(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	if strings.HasPrefix(trimmed, "[[") && strings.HasSuffix(trimmed, "]]") {
		section := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "[["), "]]"))
		if section == "" {
			return "", false
		}
		return section, true
	}
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		section := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]"))
		if section == "" {
			return "", false
		}
		return section, true
	}
	return "", false
}

func buildMissingBlock(keys []string, values map[string]interface{}) ([]string, error) {
	lines := []string{
		"# Added by construct sys doctor --fix",
	}
	for _, key := range keys {
		line, err := formatTomlLine(key, values[key])
		if err != nil {
			return nil, err
		}
		lines = append(lines, line)
	}
	return lines, nil
}

func formatTomlLine(key string, value interface{}) (string, error) {
	data, err := toml.Marshal(map[string]interface{}{key: value})
	if err != nil {
		return "", fmt.Errorf("format %s: %w", key, err)
	}
	return strings.TrimSpace(string(data)), nil
}

func backupConfig(configPath string, data []byte) error {
	backupPath := configPath + ".backup"
	if _, err := os.Stat(backupPath); err == nil {
		timestamp := time.Now().Format("20060102150405")
		rotated := backupPath + "." + timestamp
		if err := os.Rename(backupPath, rotated); err != nil {
			return fmt.Errorf("rotate backup: %w", err)
		}
	}
	if err := os.WriteFile(backupPath, data, 0644); err != nil {
		return fmt.Errorf("write backup: %w", err)
	}
	return nil
}
