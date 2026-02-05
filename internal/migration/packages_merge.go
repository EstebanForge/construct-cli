// Package migration handles configuration and template migrations.
package migration

import (
	"fmt"

	"github.com/pelletier/go-toml/v2"
)

func mergeTemplateWithBackupMissingKeys(templateData, backupData []byte) ([]byte, error) {
	var backupConfig map[string]interface{}
	if err := toml.Unmarshal(backupData, &backupConfig); err != nil {
		return nil, fmt.Errorf("parse backup config: %w", err)
	}

	var templateConfig map[string]interface{}
	if err := toml.Unmarshal(templateData, &templateConfig); err != nil {
		return nil, fmt.Errorf("parse template config: %w", err)
	}

	merged := mergeMapsMissingKeys(backupConfig, templateConfig)
	return toml.Marshal(merged)
}

func mergeMapsMissingKeys(base, overlay map[string]interface{}) map[string]interface{} {
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
			result[key] = mergeMapsMissingKeys(baseMap, overlayMap)
		}
	}

	return result
}
