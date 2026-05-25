package workflow

import (
	"reflect"
	"sort"
	"strings"

	"github.com/nikolalohinski/gonja/v2"
)

type TemplateFunctionDoc struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"` // "filter" or "test"
	Description string   `json:"description"`
	Arguments   []string `json:"arguments,omitempty"`
	Example     string   `json:"example,omitempty"`
}

var templatingDocs = map[string]TemplateFunctionDoc{
	// Encoding Filters
	"b64encode": {
		Name:        "b64encode",
		Type:        "filter",
		Description: "Base64 encodes the input string.",
		Example:     "{{ 'hello' | b64encode }}",
	},
	"b64decode": {
		Name:        "b64decode",
		Type:        "filter",
		Description: "Base64 decodes the input string.",
		Example:     "{{ 'aGVsbG8=' | b64decode }}",
	},
	"md5": {
		Name:        "md5",
		Type:        "filter",
		Description: "Calculates the MD5 hash of the input string.",
		Example:     "{{ 'hello' | md5 }}",
	},
	"sha1": {
		Name:        "sha1",
		Type:        "filter",
		Description: "Calculates the SHA1 hash of the input string.",
		Example:     "{{ 'hello' | sha1 }}",
	},
	"checksum": {
		Name:        "checksum",
		Type:        "filter",
		Description: "Calculates the checksum of the input string (defaults to sha1).",
		Example:     "{{ 'hello' | checksum }}",
	},
	"hash": {
		Name:        "hash",
		Type:        "filter",
		Description: "Calculates the hash of the input string using the specified algorithm (md5, sha1, crc32).",
		Arguments:   []string{"algorithm"},
		Example:     "{{ 'hello' | hash('md5') }}",
	},

	// Path Filters
	"basename": {
		Name:        "basename",
		Type:        "filter",
		Description: "Returns the last element of the path.",
		Example:     "{{ '/path/to/file.txt' | basename }}",
	},
	"dirname": {
		Name:        "dirname",
		Type:        "filter",
		Description: "Returns all but the last element of the path.",
		Example:     "{{ '/path/to/file.txt' | dirname }}",
	},
	"splitext": {
		Name:        "splitext",
		Type:        "filter",
		Description: "Splits the path into root and extension.",
		Example:     "{{ 'file.txt' | splitext }}",
	},
	"win_basename": {
		Name:        "win_basename",
		Type:        "filter",
		Description: "Returns the last element of a Windows path.",
		Example:     "{{ 'C:\\path\\file.txt' | win_basename }}",
	},
	"win_dirname": {
		Name:        "win_dirname",
		Type:        "filter",
		Description: "Returns the directory component of a Windows path.",
		Example:     "{{ 'C:\\path\\file.txt' | win_dirname }}",
	},
	"win_splitdrive": {
		Name:        "win_splitdrive",
		Type:        "filter",
		Description: "Splits the drive letter from the rest of the path.",
		Example:     "{{ 'C:\\path' | win_splitdrive }}",
	},
	"path_join": {
		Name:        "path_join",
		Type:        "filter",
		Description: "Joins a list of path components.",
		Example:     "{{ ['/path', 'to', 'file'] | path_join }}",
	},
	"commonpath": {
		Name:        "commonpath",
		Type:        "filter",
		Description: "Returns the longest common path prefix.",
		Example:     "{{ ['/usr/local/bin', '/usr/local/etc'] | commonpath }}",
	},
	"normpath": {
		Name:        "normpath",
		Type:        "filter",
		Description: "Clean up a path by collapsing redundant separators and up-level references.",
		Example:     "{{ '/path//to/../file' | normpath }}",
	},

	// Data Filters
	"from_json": {
		Name:        "from_json",
		Type:        "filter",
		Description: "Parses a JSON string into a variable.",
		Example:     "{{ '{\"a\": 1}' | from_json }}",
	},
	"to_nice_json": {
		Name:        "to_nice_json",
		Type:        "filter",
		Description: "Formats a variable as a JSON string with indentation.",
		Arguments:   []string{"indent"},
		Example:     "{{ var | to_nice_json(2) }}",
	},
	"to_json": {
		Name:        "to_json",
		Type:        "filter",
		Description: "Formats a variable as a compact JSON string.",
		Example:     "{{ var | to_json }}",
	},
	"from_yaml": {
		Name:        "from_yaml",
		Type:        "filter",
		Description: "Parses a YAML string into a variable.",
		Example:     "{{ 'a: 1' | from_yaml }}",
	},
	"from_yaml_all": {
		Name:        "from_yaml_all",
		Type:        "filter",
		Description: "Parses a multi-document YAML string into a list of variables.",
		Example:     "{{ yaml_string | from_yaml_all }}",
	},
	"to_yaml": {
		Name:        "to_yaml",
		Type:        "filter",
		Description: "Formats a variable as a YAML string.",
		Example:     "{{ var | to_yaml }}",
	},
	"to_nice_yaml": {
		Name:        "to_nice_yaml",
		Type:        "filter",
		Description: "Formats a variable as a YAML string with specific indentation.",
		Arguments:   []string{"indent"},
		Example:     "{{ var | to_nice_yaml(2) }}",
	},

	// List Logic Filters
	"flatten": {
		Name:        "flatten",
		Type:        "filter",
		Description: "Flattens a list of lists.",
		Example:     "{{ [[1, 2], [3, 4]] | flatten }}",
	},
	"shuffle": {
		Name:        "shuffle",
		Type:        "filter",
		Description: "Randomizes the order of elements in a list.",
		Example:     "{{ [1, 2, 3] | shuffle }}",
	},
	"ternary": {
		Name:        "ternary",
		Type:        "filter",
		Description: "Returns one of two values depending on the input boolean.",
		Arguments:   []string{"true_val", "false_val"},
		Example:     "{{ true | ternary('yes', 'no') }}",
	},
	"mandatory": {
		Name:        "mandatory",
		Type:        "filter",
		Description: "Fails if the variable is undefined or empty.",
		Arguments:   []string{"msg"},
		Example:     "{{ var | mandatory('var is required') }}",
	},
	"ans_random": {
		Name:        "ans_random",
		Type:        "filter",
		Description: "Returns a random item from a list or a random number up to a limit.",
		Example:     "{{ [1, 2, 3] | ans_random }}",
	},
	"ans_groupby": {
		Name:        "ans_groupby",
		Type:        "filter",
		Description: "Groups a list of dicts by a key.",
		Arguments:   []string{"key"},
		Example:     "{{ list | ans_groupby('category') }}",
	},
	"extract": {
		Name:        "extract",
		Type:        "filter",
		Description: "Maps a list of keys to values in a container.",
		Arguments:   []string{"container"},
		Example:     "{{ ['a', 'b'] | extract({'a': 1, 'b': 2}) }}",
	},
	"subelements": {
		Name:        "subelements",
		Type:        "filter",
		Description: "Returns a product of a list and its sublist items.",
		Arguments:   []string{"sublist_key"},
		Example:     "{{ users | subelements('groups') }}",
	},
	"union": {
		Name:        "union",
		Type:        "filter",
		Description: "Returns the union of two lists.",
		Arguments:   []string{"list2"},
		Example:     "{{ list1 | union(list2) }}",
	},
	"intersect": {
		Name:        "intersect",
		Type:        "filter",
		Description: "Returns the intersection of two lists.",
		Arguments:   []string{"list2"},
		Example:     "{{ list1 | intersect(list2) }}",
	},
	"difference": {
		Name:        "difference",
		Type:        "filter",
		Description: "Returns items in the first list but not in the second.",
		Arguments:   []string{"list2"},
		Example:     "{{ list1 | difference(list2) }}",
	},
	"symmetric_difference": {
		Name:        "symmetric_difference",
		Type:        "filter",
		Description: "Returns items in either list but not both.",
		Arguments:   []string{"list2"},
		Example:     "{{ list1 | symmetric_difference(list2) }}",
	},

	// Regex Filters
	"regex_search": {
		Name:        "regex_search",
		Type:        "filter",
		Description: "Searches for a regex pattern in the string.",
		Arguments:   []string{"pattern"},
		Example:     "{{ 'hello world' | regex_search('world') }}",
	},
	"regex_findall": {
		Name:        "regex_findall",
		Type:        "filter",
		Description: "Finds all occurrences of a regex pattern.",
		Arguments:   []string{"pattern"},
		Example:     "{{ 'abc 123 def 456' | regex_findall('\\d+') }}",
	},
	"regex_replace": {
		Name:        "regex_replace",
		Type:        "filter",
		Description: "Replaces occurrences of a regex pattern.",
		Arguments:   []string{"pattern", "replacement"},
		Example:     "{{ 'hello world' | regex_replace('world', 'universe') }}",
	},
	"regex_escape": {
		Name:        "regex_escape",
		Type:        "filter",
		Description: "Escapes special characters in a string for use in regex.",
		Example:     "{{ 'a.b' | regex_escape }}",
	},

	// Time Filters
	"strftime": {
		Name:        "strftime",
		Type:        "filter",
		Description: "Formats a time or timestamp.",
		Arguments:   []string{"format"},
		Example:     "{{ now | strftime('%Y-%m-%d') }}",
	},
	"to_datetime": {
		Name:        "to_datetime",
		Type:        "filter",
		Description: "Parses a string into a datetime object.",
		Arguments:   []string{"format"},
		Example:     "{{ '2023-01-01' | to_datetime }}",
	},
	"date_format": {
		Name:        "date_format",
		Type:        "filter",
		Description: "Formats a time using Go layout.",
		Arguments:   []string{"layout"},
		Example:     "{{ now | date_format('2006-01-02') }}",
	},
	"time_add": {
		Name:        "time_add",
		Type:        "filter",
		Description: "Adds a duration to a time.",
		Arguments:   []string{"duration"},
		Example:     "{{ now | time_add('1h') }}",
	},
	"parse_time": {
		Name:        "parse_time",
		Type:        "filter",
		Description: "Parses a string into a time using Go layout.",
		Arguments:   []string{"layout"},
		Example:     "{{ '2023-01-01' | parse_time('2006-01-02') }}",
	},

	// Misc Filters
	"to_uuid": {
		Name:        "to_uuid",
		Type:        "filter",
		Description: "Generates a UUID v5 from the input string.",
		Example:     "{{ 'my-string' | to_uuid }}",
	},
	"type_debug": {
		Name:        "type_debug",
		Type:        "filter",
		Description: "Returns the Go type of the value.",
		Example:     "{{ var | type_debug }}",
	},
	"bool": {
		Name:        "bool",
		Type:        "filter",
		Description: "Converts the value to a boolean.",
		Example:     "{{ 'true' | bool }}",
	},
	"quote": {
		Name:        "quote",
		Type:        "filter",
		Description: "Quotes the string for shell usage.",
		Example:     "{{ 'foo bar' | quote }}",
	},
	"random_mac": {
		Name:        "random_mac",
		Type:        "filter",
		Description: "Generates a random MAC address.",
		Example:     "{{ 'seed' | random_mac }}",
	},
	"comment": {
		Name:        "comment",
		Type:        "filter",
		Description: "Comments out the input text.",
		Arguments:   []string{"style"},
		Example:     "{{ 'text' | comment }}",
	},

	// Dict Filters
	"combine": {
		Name:        "combine",
		Type:        "filter",
		Description: "Merges two dictionaries.",
		Arguments:   []string{"other_dict", "recursive"},
		Example:     "{{ dict1 | combine(dict2) }}",
	},
	"dict2items": {
		Name:        "dict2items",
		Type:        "filter",
		Description: "Converts a dictionary to a list of key-value pairs.",
		Example:     "{{ {'a': 1} | dict2items }}",
	},
	"items2dict": {
		Name:        "items2dict",
		Type:        "filter",
		Description: "Converts a list of key-value pairs to a dictionary.",
		Example:     "{{ items | items2dict }}",
	},

	// URL Filters
	"urlsplit": {
		Name:        "urlsplit",
		Type:        "filter",
		Description: "Parses a URL into its components.",
		Example:     "{{ 'http://example.com' | urlsplit }}",
	},
	"urldecode": {
		Name:        "urldecode",
		Type:        "filter",
		Description: "Decodes a URL-encoded string.",
		Example:     "{{ 'foo%20bar' | urldecode }}",
	},
	"urlencode": {
		Name:        "urlencode",
		Type:        "filter",
		Description: "URL-encodes a string.",
		Example:     "{{ 'foo bar' | urlencode }}",
	},

	// Human Filters
	"human_readable": {
		Name:        "human_readable",
		Type:        "filter",
		Description: "Formats a byte count into a human-readable string.",
		Example:     "{{ 1024 | human_readable }}",
	},
	"filesizeformat": {
		Name:        "filesizeformat",
		Type:        "filter",
		Description: "Formats a byte count into a human-readable string.",
		Example:     "{{ 1024 | filesizeformat }}",
	},
	"human_to_bytes": {
		Name:        "human_to_bytes",
		Type:        "filter",
		Description: "Parses a human-readable size string into bytes.",
		Example:     "{{ '1G' | human_to_bytes }}",
	},

	// Math Filters
	"log": {
		Name:        "log",
		Type:        "filter",
		Description: "Calculates the logarithm of the input.",
		Arguments:   []string{"base"},
		Example:     "{{ 100 | log(10) }}",
	},
	"pow": {
		Name:        "pow",
		Type:        "filter",
		Description: "Raises the input to a power.",
		Arguments:   []string{"exponent"},
		Example:     "{{ 2 | pow(3) }}",
	},
	"root": {
		Name:        "root",
		Type:        "filter",
		Description: "Calculates the square root of the input.",
		Example:     "{{ 9 | root }}",
	},

	// Formatting Filters
	"wordwrap": {
		Name:        "wordwrap",
		Type:        "filter",
		Description: "Wraps text to a specified width.",
		Arguments:   []string{"width"},
		Example:     "{{ text | wordwrap(80) }}",
	},
	"center": {
		Name:        "center",
		Type:        "filter",
		Description: "Centers text within a specified width.",
		Arguments:   []string{"width", "fill_char"},
		Example:     "{{ 'hello' | center(20) }}",
	},

	// Tests
	"defined": {
		Name:        "defined",
		Type:        "test",
		Description: "Checks if a variable is defined.",
		Example:     "{{ var is defined }}",
	},
	"undefined": {
		Name:        "undefined",
		Type:        "test",
		Description: "Checks if a variable is undefined.",
		Example:     "{{ var is undefined }}",
	},
	"none": {
		Name:        "none",
		Type:        "test",
		Description: "Checks if a variable is None/Nil.",
		Example:     "{{ var is none }}",
	},
	"boolean": {
		Name:        "boolean",
		Type:        "test",
		Description: "Checks if a variable is a boolean.",
		Example:     "{{ var is boolean }}",
	},
	"number": {
		Name:        "number",
		Type:        "test",
		Description: "Checks if a variable is a number.",
		Example:     "{{ var is number }}",
	},
	"string": {
		Name:        "string",
		Type:        "test",
		Description: "Checks if a variable is a string.",
		Example:     "{{ var is string }}",
	},
	"even": {
		Name:        "even",
		Type:        "test",
		Description: "Checks if a number is even.",
		Example:     "{{ var is even }}",
	},
	"odd": {
		Name:        "odd",
		Type:        "test",
		Description: "Checks if a number is odd.",
		Example:     "{{ var is odd }}",
	},
	"iterable": {
		Name:        "iterable",
		Type:        "test",
		Description: "Checks if a variable is iterable.",
		Example:     "{{ var is iterable }}",
	},
	"mapping": {
		Name:        "mapping",
		Type:        "test",
		Description: "Checks if a variable is a dictionary/map.",
		Example:     "{{ var is mapping }}",
	},
}

// GetTemplatingDocs returns the documentation for all available templating functions (filters and tests).
func GetTemplatingDocs() (filters []TemplateFunctionDoc, tests []TemplateFunctionDoc) {
	// Helper to extract keys using reflection
	getKeys := func(source interface{}, fieldName string) []string {
		keys := make([]string, 0)
		if gonja.DefaultEnvironment == nil || source == nil {
			return keys
		}

		val := reflect.ValueOf(source)
		if val.Kind() == reflect.Ptr {
			val = val.Elem()
		}
		if val.Kind() == reflect.Struct {
			fieldMap := val.FieldByName(fieldName)
			if fieldMap.IsValid() && fieldMap.Kind() == reflect.Map {
				for _, k := range fieldMap.MapKeys() {
					keys = append(keys, k.String())
				}
			}
		}
		return keys
	}

	filterKeys := getKeys(gonja.DefaultEnvironment.Filters, "filters")
	testKeys := getKeys(gonja.DefaultEnvironment.Tests, "tests")

	// Process Filters
	for _, name := range filterKeys {
		if doc, ok := templatingDocs[name]; ok && doc.Type == "filter" {
			filters = append(filters, doc)
		} else {
			filters = append(filters, TemplateFunctionDoc{
				Name:        name,
				Type:        "filter",
				Description: "Built-in or undocumented filter.",
			})
		}
	}

	// Process Tests
	for _, name := range testKeys {
		if doc, ok := templatingDocs[name]; ok && doc.Type == "test" {
			tests = append(tests, doc)
		} else {
			tests = append(tests, TemplateFunctionDoc{
				Name:        name,
				Type:        "test",
				Description: "Built-in or undocumented test.",
			})
		}
	}

	sort.Slice(filters, func(i, j int) bool {
		return strings.Compare(filters[i].Name, filters[j].Name) < 0
	})
	sort.Slice(tests, func(i, j int) bool {
		return strings.Compare(tests[i].Name, tests[j].Name) < 0
	})

	return filters, tests
}
