// Package migration handles configuration and template migrations.
package migration

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/EstebanForge/construct-cli/internal/ui"
	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

type valueReplacement struct {
	start int
	end   int
	value []byte
}

func mergeTemplateWithBackup(templateData, backupData []byte) ([]byte, error) {
	var backupConfig map[string]interface{}
	if err := toml.Unmarshal(backupData, &backupConfig); err != nil {
		return nil, fmt.Errorf("parse backup config: %w", err)
	}

	var templateConfig map[string]interface{}
	if err := toml.Unmarshal(templateData, &templateConfig); err != nil {
		return nil, fmt.Errorf("parse template config: %w", err)
	}

	templateRanges, err := buildTemplateValueRanges(templateData)
	if err != nil {
		return nil, fmt.Errorf("parse template config: %w", err)
	}

	backupValues := flattenTomlMap(backupConfig)
	templateValues := flattenTomlMap(templateConfig)
	replacements := make([]valueReplacement, 0, len(backupValues))
	appliedKeys := make(map[string]bool)

	for key, value := range backupValues {
		templateValue, ok := templateValues[key]
		if !ok || !typesCompatible(templateValue, value) {
			continue
		}
		valueRange, ok := templateRanges[key]
		if !ok || valueRange.Length == 0 {
			continue
		}
		formatted, err := formatTomlValue(value)
		if err != nil {
			continue
		}
		start := int(valueRange.Offset)
		end := start + int(valueRange.Length)
		replacements = append(replacements, valueReplacement{
			start: start,
			end:   end,
			value: formatted,
		})
		appliedKeys[key] = true
	}

	// Calculate unapplied keys (those in backup but missing from template)
	unapplied := make(map[string]interface{})
	rootKeysInTemplate := make(map[string]bool)
	for k := range templateValues {
		rootKeysInTemplate[strings.Split(k, ".")[0]] = true
	}

	for key, value := range backupValues {
		if !appliedKeys[key] {
			// Only append if the entire root section is missing from the template.
			// This prevents duplicate [section] headers which break TOML validation.
			rootKey := strings.Split(key, ".")[0]
			if !rootKeysInTemplate[rootKey] {
				unapplied[key] = value
			}
		}
	}

	updated := append([]byte(nil), templateData...)

	// Apply replacements in reverse order
	if len(replacements) > 0 {
		sort.Slice(replacements, func(i, j int) bool {
			return replacements[i].start > replacements[j].start
		})

		for _, replacement := range replacements {
			updated = append(updated[:replacement.start], append(replacement.value, updated[replacement.end:]...)...)
		}
	}

	// Append unapplied settings (e.g. custom CC providers)
	if len(unapplied) > 0 {
		// Group by section path to produce cleaner TOML
		sections := make(map[string]map[string]interface{})
		for k, v := range unapplied {
			parts := strings.Split(k, ".")
			if len(parts) < 2 {
				// Root level key
				if _, ok := sections[""]; !ok {
					sections[""] = make(map[string]interface{})
				}
				sections[""][k] = v
				continue
			}
			sectionPath := strings.Join(parts[:len(parts)-1], ".")
			key := parts[len(parts)-1]
			if _, ok := sections[sectionPath]; !ok {
				sections[sectionPath] = make(map[string]interface{})
			}
			sections[sectionPath][key] = v
		}

		if len(sections) > 0 {
			updated = append(updated, []byte("\n\n# --- User-defined or custom settings ---\n")...)

			// Sort section names for deterministic output
			sectionNames := make([]string, 0, len(sections))
			for name := range sections {
				sectionNames = append(sectionNames, name)
			}
			sort.Strings(sectionNames)

			for _, name := range sectionNames {
				if name != "" {
					updated = append(updated, []byte(fmt.Sprintf("[%s]\n", name))...)
				}
				// Marshal keys in this section
				data, err := toml.Marshal(sections[name])
				if err == nil {
					// Clean up the marshaled data (remove newlines, use double quotes)
					clean := string(data)
					clean = strings.ReplaceAll(clean, "'", "\"")
					updated = append(updated, []byte(clean)...)
					updated = append(updated, []byte("\n")...)
				}
			}
		}
	}

	if err := validateToml(updated); err != nil {
		ui.LogDebug("Comment-preserving merge failed validation: %v. Falling back to additive merge.", err)
		fallback, fallbackErr := mergeConfigData(templateConfig, backupConfig)
		if fallbackErr != nil {
			return nil, err
		}
		return fallback, nil
	}

	return updated, nil
}

func buildTemplateValueRanges(templateData []byte) (map[string]unstable.Range, error) {
	parser := unstable.Parser{}
	parser.Reset(templateData)

	valueRanges := make(map[string]unstable.Range)
	var currentTable []string

	for parser.NextExpression() {
		expr := parser.Expression()
		switch expr.Kind {
		case unstable.Table, unstable.ArrayTable:
			currentTable = readKeyPath(expr.Key())
		case unstable.KeyValue:
			keyPath := readKeyPath(expr.Key())
			fullPath := joinPath(currentTable, keyPath)
			valueRanges[fullPath] = expr.Value().Raw
		}
	}

	if err := parser.Error(); err != nil {
		return nil, err
	}

	return valueRanges, nil
}

func readKeyPath(keys unstable.Iterator) []string {
	var parts []string
	for keys.Next() {
		node := keys.Node()
		if node.Kind != unstable.Key {
			continue
		}
		parts = append(parts, normalizeKey(string(node.Data)))
	}
	return parts
}

func normalizeKey(raw string) string {
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		if unquoted, err := strconv.Unquote(raw); err == nil {
			return unquoted
		}
	}
	if len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\'' {
		return raw[1 : len(raw)-1]
	}
	return raw
}

func joinPath(tablePath, keyPath []string) string {
	parts := append([]string{}, tablePath...)
	parts = append(parts, keyPath...)
	return strings.Join(parts, ".")
}

func flattenTomlMap(data map[string]interface{}) map[string]interface{} {
	flat := make(map[string]interface{})

	var walk func(prefix string, value interface{})
	walk = func(prefix string, value interface{}) {
		if valueMap, ok := value.(map[string]interface{}); ok {
			for key, child := range valueMap {
				next := key
				if prefix != "" {
					next = prefix + "." + key
				}
				walk(next, child)
			}
			return
		}
		if prefix != "" {
			flat[prefix] = value
		}
	}

	for key, value := range data {
		walk(key, value)
	}

	return flat
}

func formatTomlValue(value interface{}) ([]byte, error) {
	encoded, err := toml.Marshal(map[string]interface{}{"value": value})
	if err != nil {
		return nil, err
	}

	idx := bytes.IndexByte(encoded, '=')
	if idx == -1 {
		return nil, fmt.Errorf("unexpected toml encoding")
	}

	valueSection := strings.TrimSpace(string(encoded[idx+1:]))
	valueSection = strings.TrimSuffix(valueSection, "\n")
	return []byte(valueSection), nil
}

func validateToml(data []byte) error {
	var check map[string]interface{}
	return toml.Unmarshal(data, &check)
}

func mergeConfigData(templateConfig, backupConfig map[string]interface{}) ([]byte, error) {
	// Start with backup to preserve everything
	merged := make(map[string]interface{})
	for k, v := range backupConfig {
		merged[k] = v
	}

	// Overwrite/Merge with template to ensure new defaults/structure exist
	final := mergeMaps(merged, templateConfig)
	return toml.Marshal(final)
}

func mergeMaps(base, overlay map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range base {
		result[k] = v
	}

	for key, overlayValue := range overlay {
		baseValue, ok := result[key]
		if !ok {
			result[key] = overlayValue
			continue
		}

		baseMap, baseIsMap := baseValue.(map[string]interface{})
		overlayMap, overlayIsMap := overlayValue.(map[string]interface{})

		if baseIsMap && overlayIsMap {
			result[key] = mergeMaps(baseMap, overlayMap)
		} else {
			// Template (overlay) takes precedence for existing keys only if types are incompatible.
			// If compatible, we keep the base (user) value.
			if !typesCompatible(baseValue, overlayValue) {
				result[key] = overlayValue
			}
		}
	}
	return result
}

func typesCompatible(templateValue, backupValue interface{}) bool {
	if templateValue == nil || backupValue == nil {
		return false
	}

	templateType := reflect.TypeOf(templateValue)
	backupType := reflect.TypeOf(backupValue)
	if templateType == backupType {
		return true
	}

	templateKind := templateType.Kind()
	backupKind := backupType.Kind()
	if templateKind == reflect.Map && backupKind == reflect.Map {
		return true
	}
	if templateKind == reflect.Slice && backupKind == reflect.Slice {
		return true
	}
	if isNumericKind(templateKind) && isNumericKind(backupKind) {
		return true
	}

	return false
}

func isNumericKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	case reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}
