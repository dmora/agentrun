package jsonutil_test

import (
	"testing"

	"github.com/dmora/agentrun/engine/cli/internal/jsonutil"
)

func TestGetString(t *testing.T) {
	tests := []struct {
		name string
		m    map[string]any
		key  string
		want string
	}{
		{"present", map[string]any{"k": "v"}, "k", "v"},
		{"missing", map[string]any{"k": "v"}, "other", ""},
		{"wrong_type", map[string]any{"k": 42.0}, "k", ""},
		{"nil_map", nil, "k", ""},
		{"empty_string", map[string]any{"k": ""}, "k", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jsonutil.GetString(tt.m, tt.key); got != tt.want {
				t.Errorf("GetString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetInt(t *testing.T) {
	tests := []struct {
		name string
		m    map[string]any
		key  string
		want int
	}{
		{"positive", map[string]any{"k": float64(42)}, "k", 42},
		{"zero", map[string]any{"k": float64(0)}, "k", 0},
		{"negative", map[string]any{"k": float64(-5)}, "k", -5},
		{"missing", map[string]any{}, "k", 0},
		{"wrong_type_string", map[string]any{"k": "42"}, "k", 0},
		{"nil_map", nil, "k", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jsonutil.GetInt(tt.m, tt.key); got != tt.want {
				t.Errorf("GetInt() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGetFloat(t *testing.T) {
	tests := []struct {
		name string
		m    map[string]any
		key  string
		want float64
	}{
		{"positive", map[string]any{"k": 3.14}, "k", 3.14},
		{"zero", map[string]any{"k": 0.0}, "k", 0.0},
		{"negative", map[string]any{"k": -1.5}, "k", -1.5},
		{"integer_as_float", map[string]any{"k": float64(42)}, "k", 42.0},
		{"missing", map[string]any{}, "k", 0.0},
		{"wrong_type_string", map[string]any{"k": "3.14"}, "k", 0.0},
		{"nil_map", nil, "k", 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jsonutil.GetFloat(tt.m, tt.key); got != tt.want {
				t.Errorf("GetFloat() = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestGetMap(t *testing.T) {
	inner := map[string]any{"nested": "value"}
	tests := []struct {
		name    string
		m       map[string]any
		key     string
		wantNil bool
		wantKey string
		wantVal string
	}{
		{"present", map[string]any{"k": inner}, "k", false, "nested", "value"},
		{"missing", map[string]any{}, "k", true, "", ""},
		{"wrong_type_string", map[string]any{"k": "not a map"}, "k", true, "", ""},
		{"nil_map", nil, "k", true, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := jsonutil.GetMap(tt.m, tt.key)
			if tt.wantNil {
				if got != nil {
					t.Errorf("GetMap() = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("GetMap() = nil, want non-nil")
			}
			if v := got[tt.wantKey]; v != tt.wantVal {
				t.Errorf("GetMap()[%q] = %v, want %q", tt.wantKey, v, tt.wantVal)
			}
		})
	}
}

func TestContainsNull(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want bool
	}{
		{"no_null", "hello world", false},
		{"empty", "", false},
		{"null_at_start", "\x00hello", true},
		{"null_in_middle", "hel\x00lo", true},
		{"null_at_end", "hello\x00", true},
		{"only_null", "\x00", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jsonutil.ContainsNull(tt.s); got != tt.want {
				t.Errorf("ContainsNull(%q) = %v, want %v", tt.s, got, tt.want)
			}
		})
	}
}
