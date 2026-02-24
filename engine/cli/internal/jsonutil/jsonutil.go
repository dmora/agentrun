// Package jsonutil provides safe JSON extraction helpers for CLI backend
// parsers. These functions extract typed values from map[string]any produced
// by encoding/json.Unmarshal. No transformation logic, no validation.
//
// Exported within internal/ — visible to sibling packages (claude/, opencode/)
// but not to library consumers.
package jsonutil

import "strings"

// GetString safely extracts a string field from a map.
func GetString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// GetInt safely extracts a numeric field as int from a map.
// JSON numbers are decoded as float64 by encoding/json.
func GetInt(m map[string]any, key string) int {
	v, ok := m[key].(float64)
	if !ok {
		return 0
	}
	return int(v)
}

// GetFloat safely extracts a float64 field from a map.
func GetFloat(m map[string]any, key string) float64 {
	v, _ := m[key].(float64)
	return v
}

// GetMap safely extracts a nested map from a map.
func GetMap(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}

// ContainsNull reports whether s contains a null byte.
func ContainsNull(s string) bool {
	return strings.ContainsRune(s, '\x00')
}
