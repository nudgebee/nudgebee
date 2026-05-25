package workflow

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"bytes"

	"github.com/nikolalohinski/gonja/v2"
	"github.com/nikolalohinski/gonja/v2/exec"
)

func TestCustomFilters(t *testing.T) {
	tests := []struct {
		name          string
		template      string
		context       map[string]interface{}
		expected      string
		checkContains bool // if true, assert.Contains instead of Equal
	}{
		// Encoding/Hashing
		{"b64encode", `{{ "hello" | b64encode }}`, nil, "aGVsbG8=", false},
		{"b64decode", `{{ "aGVsbG8=" | b64decode }}`, nil, "hello", false},
		{"md5", `{{ "hello" | md5 }}`, nil, "5d41402abc4b2a76b9719d911017c592", false},
		{"sha1", `{{ "hello" | sha1 }}`, nil, "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d", false},
		{"checksum", `{{ "hello" | checksum }}`, nil, "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d", false}, // defaults to sha1
		{"hash_md5", `{{ "hello" | hash("md5") }}`, nil, "5d41402abc4b2a76b9719d911017c592", false},

		// JSON & YAML
		{"from_json", `{{ '{"a": 1}' | from_json | to_nice_json }}`, nil, "{\n    \"a\": 1\n}", false}, // using to_nice_json default indent 4
		{"to_nice_json", `{{ {"a": 1} | to_nice_json(2) }}`, nil, "{\n  \"a\": 1\n}", false},
		{"to_yaml", `{{ {"a": 1} | to_yaml }}`, nil, "a: 1\n", false},
		{"from_yaml", `{{ 'a: 1' | from_yaml | to_json }}`, nil, `{"a":1}`, false},

		// Path
		{"basename", `{{ "/path/to/file.txt" | basename }}`, nil, "file.txt", false},
		{"dirname", `{{ "/path/to/file.txt" | dirname }}`, nil, "/path/to", false},
		{"splitext_root", `{{ "/path/to/file.txt" | splitext | first }}`, nil, "/path/to/file", false},
		{"splitext_ext", `{{ "/path/to/file.txt" | splitext | last }}`, nil, ".txt", false},
		{"realpath", `{{ "file.txt" | realpath | basename }}`, nil, "file.txt", false},
		{"win_basename", `{{ "C:\\path\\to\\file.txt" | win_basename }}`, nil, "file.txt", false},
		{"win_dirname", `{{ "C:\\path\\to\\file.txt" | win_dirname }}`, nil, "C:\\path\\to", false},
		{"win_splitdrive_drive", `{{ "C:\\path\\to\\file.txt" | win_splitdrive | first }}`, nil, "C:", false},

		// List/Logic
		{"flatten", `{{ [[1, 2], [3]] | flatten | to_nice_json(0) }}`, nil, "[\n1,\n2,\n3\n]", false},
		{"ternary_true", `{{ true | ternary("yes", "no") }}`, nil, "yes", false},
		{"ternary_false", `{{ false | ternary("yes", "no") }}`, nil, "no", false},
		{"ternary_non_bool_true", `{{ "true" | ternary("yes", "no") }}`, nil, "yes", false},
		{"ternary_non_bool_false", `{{ "" | ternary("yes", "no") }}`, nil, "no", false},
		{"bool_true", `{{ "yes" | bool }}`, nil, "True", false},
		{"mandatory", `{{ "val" | mandatory }}`, nil, "val", false},

		// List Set Theory
		{"union", `{{ [1, 2] | union([2, 3]) | sort | join(",") }}`, nil, "1,2,3", false},
		{"intersect", `{{ [1, 2] | intersect([2, 3]) | first }}`, nil, "2", false},
		{"difference", `{{ [1, 2] | difference([2, 3]) | first }}`, nil, "1", false},
		{"symmetric_difference", `{{ [1, 2] | symmetric_difference([2, 3]) | sort | join(",") }}`, nil, "1,3", false},

		// Random
		{"ans_random_list", `{{ [1] | ans_random }}`, nil, "1", false},

		// Regex
		{"regex_replace", `{{ "foobar" | regex_replace("foo", "bar") }}`, nil, "barbar", false},
		{"regex_search", `{{ "foobar" | regex_search("foo") }}`, nil, "foo", false},
		{"regex_findall", `{{ "foo bar baz" | regex_findall("\\w+") | length }}`, nil, "3", false},
		{"regex_escape", `{{ "a.b" | regex_escape }}`, nil, `a\.b`, false},

		// Misc
		{"quote", `{{ "don't" | quote }}`, nil, "'don'\\''t'", false},
		{"comment", `{{ "hello" | comment }}`, nil, "# hello", false},
		{"type_debug", `{{ "s" | type_debug }}`, nil, "string", false},
		{"to_uuid", `{{ "test" | to_uuid }}`, nil, "da5b8893-d6ca-5c1c-9a9c-91f40a2a3649", false},

		// New Dict Manipulation
		{"combine", `{{ {"a": 1} | combine({"b": 2}) | to_nice_json(0) }}`, nil, "{\n\"a\": 1,\n\"b\": 2\n}", false},
		{"combine_override", `{{ {"a": 1} | combine({"a": 2}) | to_nice_json(0) }}`, nil, "{\n\"a\": 2\n}", false},
		{"dict2items", `{{ {"a": 1} | dict2items | first | to_json }}`, nil, `{"key":"a","value":1}`, false},
		{"items2dict", `{{ [{"key":"a","value":1}] | items2dict | to_nice_json(0) }}`, nil, "{\n\"a\": 1\n}", false},

		// URL/Network
		// attr("netloc") relies on attribute lookup which might be strict in Gonja.
		// urlsplit returns a map[string]any. Simple dot notation should work in Jinja/Gonja.
		// "http://example.com/path" | urlsplit returns {netloc: "example.com", ...}
		// ( ... | urlsplit).netloc
		{"urlsplit_host", `{{ ("http://example.com/path" | urlsplit).netloc }}`, nil, "example.com", false},
		{"urlencode", `{{ "a b" | urlencode }}`, nil, "a+b", false},
		{"urldecode", `{{ "a+b" | urldecode }}`, nil, "a b", false},

		// Human Formats
		{"human_readable", `{{ 1024 | human_readable }}`, nil, "1.0 kB", false}, // humanize defaults to IEC? or SI? Wait. go-humanize Bytes(1024) -> 1.0 kB.
		{"human_to_bytes", `{{ "1kB" | human_to_bytes }}`, nil, "1000", false},  // "1kB" is 1000, "1KiB" is 1024. humanize.ParseBytes supports both.

		// Path Utils
		{"path_join", `{{ ["/a", "b"] | path_join }}`, nil, "/a/b", false},
		{"commonpath", `{{ ["/usr/local/bin", "/usr/local/etc"] | commonpath }}`, nil, "/usr/local", false},
		{"normpath", `{{ "/a/../b" | normpath }}`, nil, "/b", false},

		// Math
		{"log10", `{{ 100.0 | log }}`, nil, "2", true},
		{"pow", `{{ 2.0 | pow(3) }}`, nil, "8", true},
		{"root", `{{ 9.0 | root }}`, nil, "3", true},

		// Fixes
		{"wordwrap", `{{ "hello world" | wordwrap(5) }}`, nil, "hello\nworld", false},
		{"center", `{{ "a" | center(3) }}`, nil, " a ", false},

		// Time
		{"strftime", `{{ 1609459200 | strftime("%Y-%m-%d") }}`, nil, "2021-01-01", false},
		{"to_datetime", `{{ "2021-01-01T00:00:00Z" | to_datetime | strftime("%Y-%m-%d") }}`, nil, "2021-01-01", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tpl, err := gonja.FromString(tt.template)
			assert.NoError(t, err)

			var out bytes.Buffer
			err = tpl.Execute(&out, exec.NewContext(tt.context))
			if err != nil {
				t.Fatalf("Execute failed: %v", err)
			}

			if tt.checkContains {
				assert.Contains(t, out.String(), tt.expected)
			} else {
				expected := strings.ReplaceAll(tt.expected, "\r\n", "\n")
				actual := strings.ReplaceAll(out.String(), "\r\n", "\n")
				assert.Equal(t, expected, actual)
			}
		})
	}

	t.Run("date_format", func(t *testing.T) {
		ctx := NewTemplateContext(nil, nil)

		// Test with time object
		fixedTime, _ := time.Parse(time.RFC3339, "2023-10-05T10:00:00Z")
		ctx.Vars["my_time"] = fixedTime

		// Default format (RFC3339)
		tpl := "{{ Vars.my_time | date_format }}"
		res, err := ctx.renderGonja(tpl)
		assert.NoError(t, err)
		assert.Equal(t, "2023-10-05T10:00:00Z", res)

		// Custom format
		tpl = "{{ Vars.my_time | date_format('2006-01-02') }}"
		res, err = ctx.renderGonja(tpl)
		assert.NoError(t, err)
		assert.Equal(t, "2023-10-05", res)

		// Test with string input
		ctx.Vars["my_time_str"] = "2023-10-05T10:00:00Z"
		tpl = "{{ Vars.my_time_str | date_format('2006-01-02') }}"
		res, err = ctx.renderGonja(tpl)
		assert.NoError(t, err)
		assert.Equal(t, "2023-10-05", res)

		// Test with invalid string input (should return original string)
		ctx.Vars["invalid_time"] = "not-a-date"
		tpl = "{{ Vars.invalid_time | date_format('2006-01-02') }}"
		res, err = ctx.renderGonja(tpl)
		assert.NoError(t, err)
		assert.Equal(t, "not-a-date", res)
	})

	t.Run("time_add", func(t *testing.T) {
		ctx := NewTemplateContext(nil, nil)

		fixedTime, _ := time.Parse(time.RFC3339, "2023-10-05T10:00:00Z")
		ctx.Vars["my_time"] = fixedTime

		// Add duration
		tpl := "{{ Vars.my_time | time_add('1h') | date_format }}"
		res, err := ctx.renderGonja(tpl)
		assert.NoError(t, err)
		assert.Equal(t, "2023-10-05T11:00:00Z", res)

		// Add days
		tpl = "{{ Vars.my_time | time_add('2d') | date_format }}"
		res, err = ctx.renderGonja(tpl)
		assert.NoError(t, err)
		assert.Equal(t, "2023-10-07T10:00:00Z", res)

		// Subtract duration
		tpl = "{{ Vars.my_time | time_add('-24h') | date_format }}"
		res, err = ctx.renderGonja(tpl)
		assert.NoError(t, err)
		assert.Equal(t, "2023-10-04T10:00:00Z", res)

		// Test with string input
		ctx.Vars["my_time_str"] = "2023-10-05T10:00:00Z"
		tpl = "{{ Vars.my_time_str | time_add('1h') | date_format }}"
		res, err = ctx.renderGonja(tpl)
		assert.NoError(t, err)
		assert.Equal(t, "2023-10-05T11:00:00Z", res)
	})

	t.Run("parse_time", func(t *testing.T) {
		ctx := NewTemplateContext(nil, nil)

		// Parse with default layout (RFC3339)
		tpl := "{{ '2023-10-05T10:00:00Z' | parse_time | date_format }}"
		res, err := ctx.renderGonja(tpl)
		assert.NoError(t, err)
		assert.Equal(t, "2023-10-05T10:00:00Z", res)

		// Parse with custom layout
		tpl = "{{ '2023-10-05' | parse_time('2006-01-02') | date_format('2006-01-02') }}"
		res, err = ctx.renderGonja(tpl)
		assert.NoError(t, err)
		assert.Equal(t, "2023-10-05", res)

		// Parse failure (returns zero time)
		tpl = "{{ 'invalid' | parse_time | date_format }}"
		res, err = ctx.renderGonja(tpl)
		assert.NoError(t, err)
		// Zero time formatted as RFC3339 is usually "0001-01-01T00:00:00Z"
		assert.Equal(t, "0001-01-01T00:00:00Z", res)
	})

	t.Run("now", func(t *testing.T) {
		ctx := NewTemplateContext(nil, nil)

		// Just check that it runs and returns a date-like string
		tpl := "{{ now() | date_format('2006-01-02') }}"
		res, err := ctx.renderGonja(tpl)
		assert.NoError(t, err)

		// Check pattern instead of exact match to avoid time race conditions
		assert.Regexp(t, `^\d{4}-\d{2}-\d{2}$`, res)
	})

	t.Run("map_keys_join", func(t *testing.T) {
		ctx := NewTemplateContext(nil, nil)
		ctx.Inputs["test_map"] = map[string]any{
			"key1": "value1",
			"key2": "value2",
			"key3": "value3",
		}

		tpl := "{{ Inputs.test_map.keys() | join(',') }}"
		res, err := ctx.renderGonja(tpl)
		assert.NoError(t, err)

		// Map keys order is not guaranteed, so check for presence and correct number of elements
		parts := strings.Split(res, ",")
		assert.Len(t, parts, 3)
		assert.Contains(t, parts, "key1")
		assert.Contains(t, parts, "key2")
		assert.Contains(t, parts, "key3")
	})

	t.Run("error_handling", func(t *testing.T) {
		// Test that panics in filters result in errors
		tests := []struct {
			name     string
			template string
		}{
			{"b64decode_invalid", `{{ "invalid_b64" | b64decode }}`},
			{"time_add_invalid", `{{ "2023-01-01T00:00:00Z" | time_add("invalid") }}`},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				ctx := NewTemplateContext(nil, nil)
				_, err := ctx.renderGonja(tt.template)
				assert.Error(t, err)
			})
		}
	})

	t.Run("subelements", func(t *testing.T) {
		ctx := NewTemplateContext(nil, nil)
		// Simple subelements test
		// [{name: a, sub: [1, 2]}, {name: b, sub: [3]}]
		// -> [[{name: a, ...}, 1], [{name: a, ...}, 2], [{name: b, ...}, 3]]
		ctx.Vars["list"] = []interface{}{
			map[string]interface{}{"name": "a", "sub": []interface{}{1, 2}},
			map[string]interface{}{"name": "b", "sub": []interface{}{3}},
		}

		tpl := "{{ Vars.list | subelements('sub') | to_json }}"
		res, err := ctx.renderGonja(tpl)
		assert.NoError(t, err)
		assert.Contains(t, res, `1`)
		assert.Contains(t, res, `2`)
		assert.Contains(t, res, `3`)

		// Missing key should be skipped or handled without panic
		ctx.Vars["list_bad"] = []interface{}{
			map[string]interface{}{"name": "a"}, // missing 'sub'
		}
		tpl = "{{ Vars.list_bad | subelements('sub') | to_json }}"
		res, err = ctx.renderGonja(tpl)
		assert.NoError(t, err)
		// result should be empty list []
		assert.Equal(t, "null", res) // or empty list depending on impl
	})
}
