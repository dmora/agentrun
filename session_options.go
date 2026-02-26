package agentrun

import (
	"fmt"
	"strconv"
	"strings"
)

// StringOption returns the value for key in opts, or defaultVal if the key
// is absent or empty.
func StringOption(opts map[string]string, key, defaultVal string) string {
	if v := opts[key]; v != "" {
		return v
	}
	return defaultVal
}

// ParsePositiveIntOption returns the integer value for key in opts.
// If the key is absent or empty, it returns (0, false, nil).
// If the value is present but not a valid positive integer, or contains
// null bytes, it returns an error.
func ParsePositiveIntOption(opts map[string]string, key string) (int, bool, error) {
	v := opts[key]
	if v == "" {
		return 0, false, nil
	}
	if strings.Contains(v, "\x00") {
		return 0, false, fmt.Errorf("option %s: value contains null bytes", key)
	}
	v = strings.TrimSpace(v)
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false, fmt.Errorf("option %s: %q is not a valid integer", key, v)
	}
	if n <= 0 {
		return 0, false, fmt.Errorf("option %s: %q must be a positive integer", key, v)
	}
	return n, true, nil
}

// ValidateEnv checks all keys and values in env.
// Keys must be non-empty and must not contain '=' or null bytes.
// Values must not contain null bytes.
// Returns the first validation error encountered.
// Nil or empty env is valid.
func ValidateEnv(env map[string]string) error {
	for k, v := range env {
		if k == "" {
			return fmt.Errorf("env key must not be empty")
		}
		if strings.ContainsRune(k, '=') {
			return fmt.Errorf("env key %q must not contain '='", k)
		}
		if strings.ContainsRune(k, '\x00') {
			return fmt.Errorf("env key %q contains null byte", k)
		}
		if strings.Contains(v, "\x00") {
			return fmt.Errorf("env value for key %q contains null byte", k)
		}
	}
	return nil
}

// ParseListOption splits a newline-separated option value into individual
// entries. Empty entries and entries containing null bytes are skipped.
// Returns nil when the key is absent or empty.
func ParseListOption(opts map[string]string, key string) []string {
	v := opts[key]
	if v == "" {
		return nil
	}
	parts := strings.Split(v, "\n")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || strings.Contains(p, "\x00") {
			continue
		}
		result = append(result, p)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// ParseBoolOption returns the boolean value for key in opts.
// If the key is absent or empty, it returns (false, false, nil).
// Truthy values: "true", "on", "1", "yes" (case-insensitive).
// Falsy values: "false", "off", "0", "no" (case-insensitive).
// Unrecognized values return an error.
func ParseBoolOption(opts map[string]string, key string) (bool, bool, error) {
	v := opts[key]
	if v == "" {
		return false, false, nil
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "on", "1", "yes":
		return true, true, nil
	case "false", "off", "0", "no":
		return false, true, nil
	default:
		return false, false, fmt.Errorf("option %s: %q is not a recognized boolean value", key, v)
	}
}
