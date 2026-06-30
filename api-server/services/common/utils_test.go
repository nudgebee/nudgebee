package common

import (
	stdjson "encoding/json"
	"testing"
)

func strPtr(s string) *string { return &s }

func TestIsEmptyStringPointer(t *testing.T) {
	empty := ""
	nonEmpty := "x"
	tests := []struct {
		name string
		in   *string
		want bool
	}{
		{name: "nil pointer", in: nil, want: true},
		{name: "pointer to empty string", in: &empty, want: true},
		{name: "pointer to non-empty string", in: &nonEmpty, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsEmptyStringPointer(tt.in); got != tt.want {
				t.Errorf("IsEmptyStringPointer() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStrVal(t *testing.T) {
	if got := StrVal(nil); got != "" {
		t.Errorf("StrVal(nil) = %q, want \"\"", got)
	}
	if got := StrVal(strPtr("hello")); got != "hello" {
		t.Errorf("StrVal(ptr) = %q, want %q", got, "hello")
	}
}

func TestGetString(t *testing.T) {
	m := map[string]any{"s": "val", "n": 42}
	tests := []struct {
		name string
		m    map[string]any
		key  string
		want string
	}{
		{name: "string value", m: m, key: "s", want: "val"},
		{name: "non-string value", m: m, key: "n", want: ""},
		{name: "missing key", m: m, key: "missing", want: ""},
		{name: "nil map", m: nil, key: "s", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetString(tt.m, tt.key); got != tt.want {
				t.Errorf("GetString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMapFlatten(t *testing.T) {
	in := map[string]interface{}{
		"a": "1",
		"b": map[string]interface{}{
			"c": "2",
			"d": 3,
		},
	}
	out := map[string]string{}
	MapFlatten("", in, out)

	want := map[string]string{
		"a":   "1",
		"b.c": "2",
		"b.d": "3",
	}
	if len(out) != len(want) {
		t.Fatalf("MapFlatten() produced %d keys, want %d: %v", len(out), len(want), out)
	}
	for k, v := range want {
		if out[k] != v {
			t.Errorf("MapFlatten()[%q] = %q, want %q", k, out[k], v)
		}
	}
}

func TestIsAlphaNumeric(t *testing.T) {
	cases := map[byte]bool{
		'a': true, 'Z': true, '0': true, '9': true, '_': true,
		' ': false, '-': false, '{': false, '\'': false,
	}
	for c, want := range cases {
		if got := isAlphaNumeric(c); got != want {
			t.Errorf("isAlphaNumeric(%q) = %v, want %v", c, got, want)
		}
	}
}

func TestConvertPythonDictToJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "literals and single quotes",
			in:   "{'a': True, 'b': False, 'c': None, 'd': 'hello'}",
			want: `{"a": true, "b": false, "c": null, "d": "hello"}`,
		},
		{
			name: "True as substring is not replaced",
			in:   "{'k': 'TrueValue'}",
			want: `{"k": "TrueValue"}`,
		},
		{
			name: "double quote inside single-quoted string is escaped",
			in:   `{'k': 'say "hi"'}`,
			want: `{"k": "say \"hi\""}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ConvertPythonDictToJSON(tt.in)
			if err != nil {
				t.Fatalf("ConvertPythonDictToJSON() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ConvertPythonDictToJSON(%q) = %q, want %q", tt.in, got, tt.want)
			}
			// Result must be valid JSON.
			var v any
			if err := stdjson.Unmarshal([]byte(got), &v); err != nil {
				t.Errorf("result %q is not valid JSON: %v", got, err)
			}
		})
	}
}
