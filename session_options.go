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
