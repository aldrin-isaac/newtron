package configlet

import (
	"fmt"
	"strings"
)

// ResolveVariables replaces all {{var}} placeholders in s with values from vars.
func ResolveVariables(s string, vars map[string]string) string {
	result := s
	for k, v := range vars {
		placeholder := "{{" + k + "}}"
		result = strings.ReplaceAll(result, placeholder, v)
	}
	return result
}

// ResolveConfiglet applies variable substitution to all keys and values
// in a configlet's config_db, returning a fully resolved config_db map.
func ResolveConfiglet(c *Configlet, vars map[string]string) map[string]map[string]map[string]string {
	result := make(map[string]map[string]map[string]string)

	for table, entries := range c.ConfigDB {
		resolvedTable := make(map[string]map[string]string)
		for key, value := range entries {
			resolvedKey := ResolveVariables(key, vars)
			fields := make(map[string]string)
			switch v := value.(type) {
			case map[string]interface{}:
				for fk, fv := range v {
					fields[fk] = ResolveVariables(fmt.Sprintf("%v", fv), vars)
				}
			case map[string]string:
				for fk, fv := range v {
					fields[fk] = ResolveVariables(fv, vars)
				}
			}
			resolvedTable[resolvedKey] = fields
		}
		result[table] = resolvedTable
	}

	return result
}

// MergeConfigDB deep-merges overlay into base at the table -> key -> field level.
// Fields in overlay overwrite fields in base for the same table+key.
func MergeConfigDB(base, overlay map[string]map[string]map[string]string) map[string]map[string]map[string]string {
	if base == nil {
		base = make(map[string]map[string]map[string]string)
	}

	for table, entries := range overlay {
		if base[table] == nil {
			base[table] = make(map[string]map[string]string)
		}
		for key, fields := range entries {
			if base[table][key] == nil {
				base[table][key] = make(map[string]string)
			}
			for fk, fv := range fields {
				base[table][key][fk] = fv
			}
		}
	}

	return base
}
