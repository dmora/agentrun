package agentrun

import (
	"math"
	"strconv"
	"testing"
)

func TestStringOption(t *testing.T) {
	tests := []struct {
		name       string
		opts       map[string]string
		key        string
		defaultVal string
		want       string
	}{
		{"present", map[string]string{"k": "v"}, "k", "d", "v"},
		{"absent", map[string]string{}, "k", "d", "d"},
		{"empty_value", map[string]string{"k": ""}, "k", "d", "d"},
		{"nil_map", nil, "k", "d", "d"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StringOption(tt.opts, tt.key, tt.defaultVal)
			if got != tt.want {
				t.Errorf("StringOption() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParsePositiveIntOption(t *testing.T) {
	tests := []struct {
		name    string
		opts    map[string]string
		key     string
		wantN   int
		wantOK  bool
		wantErr bool
	}{
		{"valid", map[string]string{"k": "42"}, "k", 42, true, false},
		{"absent", map[string]string{}, "k", 0, false, false},
		{"empty", map[string]string{"k": ""}, "k", 0, false, false},
		{"nil_map", nil, "k", 0, false, false},
		{"whitespace_padded", map[string]string{"k": " 10 "}, "k", 10, true, false},
		{"zero", map[string]string{"k": "0"}, "k", 0, false, true},
		{"negative", map[string]string{"k": "-5"}, "k", 0, false, true},
		{"not_a_number", map[string]string{"k": "abc"}, "k", 0, false, true},
		{"float", map[string]string{"k": "3.14"}, "k", 0, false, true},
		{"max_int", map[string]string{"k": strconv.Itoa(math.MaxInt)}, "k", math.MaxInt, true, false},
		{"overflow", map[string]string{"k": "99999999999999999999"}, "k", 0, false, true},
		{"null_bytes", map[string]string{"k": "12\x003"}, "k", 0, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, ok, err := ParsePositiveIntOption(tt.opts, tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if n != tt.wantN {
				t.Errorf("n = %d, want %d", n, tt.wantN)
			}
		})
	}
}

func TestParseBoolOption(t *testing.T) {
	tests := []struct {
		name    string
		opts    map[string]string
		key     string
		wantV   bool
		wantOK  bool
		wantErr bool
	}{
		// Truthy values.
		{"true", map[string]string{"k": "true"}, "k", true, true, false},
		{"TRUE", map[string]string{"k": "TRUE"}, "k", true, true, false},
		{"True", map[string]string{"k": "True"}, "k", true, true, false},
		{"on", map[string]string{"k": "on"}, "k", true, true, false},
		{"ON", map[string]string{"k": "ON"}, "k", true, true, false},
		{"1", map[string]string{"k": "1"}, "k", true, true, false},
		{"yes", map[string]string{"k": "yes"}, "k", true, true, false},
		{"Yes", map[string]string{"k": "Yes"}, "k", true, true, false},
		// Falsy values.
		{"false", map[string]string{"k": "false"}, "k", false, true, false},
		{"FALSE", map[string]string{"k": "FALSE"}, "k", false, true, false},
		{"off", map[string]string{"k": "off"}, "k", false, true, false},
		{"0", map[string]string{"k": "0"}, "k", false, true, false},
		{"no", map[string]string{"k": "no"}, "k", false, true, false},
		// Absent / empty.
		{"absent", map[string]string{}, "k", false, false, false},
		{"empty", map[string]string{"k": ""}, "k", false, false, false},
		{"nil_map", nil, "k", false, false, false},
		// Unrecognized → error.
		{"unrecognized", map[string]string{"k": "maybe"}, "k", false, false, true},
		{"unrecognized_enabled", map[string]string{"k": "enabled"}, "k", false, false, true},
		// Whitespace.
		{"whitespace_true", map[string]string{"k": " true "}, "k", true, true, false},
		{"whitespace_off", map[string]string{"k": " OFF "}, "k", false, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, ok, err := ParseBoolOption(tt.opts, tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if v != tt.wantV {
				t.Errorf("v = %v, want %v", v, tt.wantV)
			}
		})
	}
}

func FuzzParsePositiveIntOption(f *testing.F) {
	f.Add("42")
	f.Add("")
	f.Add("abc")
	f.Add("-1")
	f.Add("0")
	f.Add("99999999999999999999")
	f.Add(" 10 ")
	f.Add("\x0042")

	f.Fuzz(func(t *testing.T, val string) {
		opts := map[string]string{"k": val}
		n, ok, err := ParsePositiveIntOption(opts, "k")
		if err != nil && ok {
			t.Error("error and ok should not both be true")
		}
		if ok && n <= 0 {
			t.Errorf("ok=true but n=%d (should be positive)", n)
		}
	})
}

func FuzzParseBoolOption(f *testing.F) {
	f.Add("true")
	f.Add("false")
	f.Add("")
	f.Add("maybe")
	f.Add("ON")
	f.Add(" yes ")

	f.Fuzz(func(_ *testing.T, val string) {
		opts := map[string]string{"k": val}
		_, _, _ = ParseBoolOption(opts, "k")
	})
}
