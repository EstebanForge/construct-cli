// Package migration handles configuration and template migrations.
package migration

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

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
	}

	if len(replacements) == 0 {
		return templateData, nil
	}

	sort.Slice(replacements, func(i, j int) bool {
		return replacements[i].start > replacements[j].start
	})

	updated := append([]byte(nil), templateData...)
	for _, replacement := range replacements {
		updated = append(updated[:replacement.start], append(replacement.value, updated[replacement.end:]...)...)
	}

	if err := validateToml(updated); err != nil {
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
	merged := mergeMaps(templateConfig, backupConfig)
	return toml.Marshal(merged)
}

func mergeMaps(templateConfig, backupConfig map[string]interface{}) map[string]interface{} {
	merged := make(map[string]interface{}, len(templateConfig))
	for key, templateValue := range templateConfig {
		backupValue, ok := backupConfig[key]
		if !ok {
			merged[key] = templateValue
			continue
		}
		templateMap, templateIsMap := templateValue.(map[string]interface{})
		backupMap, backupIsMap := backupValue.(map[string]interface{})
		if templateIsMap && backupIsMap {
			merged[key] = mergeMaps(templateMap, backupMap)
			continue
		}
		if typesCompatible(templateValue, backupValue) {
			merged[key] = backupValue
		} else {
			merged[key] = templateValue
		}
	}
	return merged
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
