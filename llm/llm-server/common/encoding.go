package common

import (
	"errors"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	jsoniter "github.com/json-iterator/go"
	"github.com/mitchellh/mapstructure"
	"gopkg.in/yaml.v3"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

func stringToTimeHookFunc() mapstructure.DecodeHookFunc {
	return func(
		f reflect.Type,
		t reflect.Type,
		data any) (any, error) {
		if f.Kind() != reflect.String {
			return data, nil
		}

		isTime := t == reflect.TypeOf(time.Time{})
		isTimePtr := t.Kind() == reflect.Ptr && t.Elem() == reflect.TypeOf(time.Time{})

		if !isTime && !isTimePtr {
			return data, nil
		}

		data1, ok := data.(string)
		if !ok {
			return data, nil
		}
		if data1 == "" {
			if isTimePtr {
				return nil, nil
			}
			return time.Time{}, nil
		}

		layouts := []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02T15:04:05.999999999Z0700",
			"2006-01-02T15:04:05Z0700",
			"2006-01-02T15:04:05.999999999",
			"2006-01-02T15:04:05",
			"2006-01-02 15:04:05",
			"2006-01-02",
		}

		var parsedTime time.Time
		var err error
		for _, layout := range layouts {
			parsedTime, err = time.ParseInLocation(layout, data1, time.UTC)
			if err == nil {
				if isTimePtr {
					return &parsedTime, nil
				}
				return parsedTime, nil
			}
		}

		return nil, err
	}
}

func DecodeMapToStruct(m map[string]any, s any) error {
	// if s is not pointer then throw error
	if s == nil {
		return errors.New("DecodeMapToStruct: s is nil")
	}

	if reflect.TypeOf(s).Kind() != reflect.Ptr {
		return errors.New("DecodeMapToStruct: s is not a pointer")
	}

	decoderConfig := mapstructure.DecoderConfig{
		TagName: "json",
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			stringToTimeHookFunc(),
			mapstructure.StringToIPHookFunc(),
			mapstructure.StringToIPNetHookFunc(),
			mapstructure.StringToTimeDurationHookFunc(),
			mapstructure.StringToSliceHookFunc(","),
		),
		Result:           s,
		WeaklyTypedInput: true, // This handles int to string conversion automatically
	}

	decoder, err := mapstructure.NewDecoder(&decoderConfig)
	if err != nil {
		return err
	}

	return decoder.Decode(m)
}

func EncodeToJsonSafe(data any) string {
	if data == nil {
		return ""
	}
	str, err := MarshalJson(data)
	if err != nil {
		panic(err)
	}

	return string(str)
}

func DecodeYamlToMap(data string) (map[string]any, error) {
	var result map[string]any
	err := yaml.Unmarshal([]byte(data), &result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func EncodeYaml(data any) ([]byte, error) {
	result, err := yaml.Marshal(data)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func StructToMap(obj any) (map[string]any, error) {
	objValue := reflect.ValueOf(obj)
	if objValue.Kind() != reflect.Struct {
		return nil, errors.New("DecodeMapToStruct: s is nil")
	}

	objType := objValue.Type()
	result := make(map[string]any)

	for i := 0; i < objType.NumField(); i++ {
		field := objType.Field(i)
		fieldValue := objValue.Field(i).Interface()
		result[field.Name] = fieldValue
	}

	return result, nil
}

func MarshalJson(obj any) ([]byte, error) {
	return json.Marshal(obj)
}

func MarshalJsonIndent(obj any, prefix, indent string) ([]byte, error) {
	return json.MarshalIndent(obj, prefix, indent)
}

func UnmarshalJson(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// UnmarshalJsonAsMap decodes arbitrary JSON into a map[string]any without losing
// data on arrays or other top-level shapes:
//   - JSON object → returned as-is.
//   - JSON array  → wrapped as {"items": <array>}.
//   - Other valid JSON (string/number/bool/null) → wrapped as {"value": <v>}.
//
// Use this instead of `json.Unmarshal(data, &map[string]any{})` when the producer
// may emit either an object or an array and you don't want callers to crash or
// silently truncate. Returns an error only if the input is not valid JSON.
func UnmarshalJsonAsMap(data []byte) (map[string]any, error) {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	switch v := raw.(type) {
	case map[string]any:
		return v, nil
	case []any:
		return map[string]any{"items": v}, nil
	default:
		return map[string]any{"value": v}, nil
	}
}

var markdownJsonRegex = regexp.MustCompile("(?s)```(?:json)?\n?(.*?)\n?```")

// ExtractAndUnmarshalJSON attempts to unmarshal JSON data by being robust against surrounding text,
// markdown fences, and multiple embedded JSON structures. It tries various extraction
// strategies in order of likelihood.
func ExtractAndUnmarshalJSON(data []byte, v any) error {
	// 1. Try direct unmarshal first (fastest and most correct for clean input)
	originalErr := UnmarshalJson(data, v)
	if originalErr == nil {
		val := reflect.ValueOf(v)
		if val.Kind() == reflect.Ptr {
			val = val.Elem()
		}
		if !val.IsZero() {
			return nil
		}
		// It's a zero value, so we should continue to find a better match.
		originalErr = errors.New("json unmarshalled to zero value")
	}

	text := string(data)
	tried := make(map[string]bool)
	tried[text] = true

	// 2. Try Markdown fenced JSON blocks (common in LLM outputs)
	if strings.Contains(text, "```") {
		matches := markdownJsonRegex.FindAllStringSubmatch(text, -1)
		for _, match := range matches {
			if len(match) > 1 {
				candidate := strings.TrimSpace(match[1])
				if candidate != "" && !tried[candidate] {
					if err := UnmarshalJson([]byte(candidate), v); err == nil {
						val := reflect.ValueOf(v)
						if val.Kind() == reflect.Ptr {
							val = val.Elem()
						}
						// Only return if unmarshalling resulted in a non-zero value.
						if !val.IsZero() {
							return nil
						}

					}
					tried[candidate] = true
				}
			}
		}
	}

	// 3. Try balanced JSON blocks (including nested ones, ordered by outermost first)
	for _, candidate := range extractBalancedBlocks(text) {
		if !tried[candidate] {
			if err := UnmarshalJson([]byte(candidate), v); err == nil {
				val := reflect.ValueOf(v)
				if val.Kind() == reflect.Ptr {
					val = val.Elem()
				}
				// Only return if unmarshalling resulted in a non-zero value.
				if !val.IsZero() {
					return nil
				}
			}
			tried[candidate] = true
		}
	}

	// If everything fails, return the error from the original full data
	return originalErr
}

// extractBalancedBlocks finds all substrings that start with { or [ and end with their
// matching balanced counterpart in O(N) time. It prioritizes outermost and earlier blocks.
func extractBalancedBlocks(text string) []string {
	match := make(map[int]int)
	var stack []int
	inString := false
	escaped := false

	// One pass to find all balanced pairs
	for i := 0; i < len(text); i++ {
		char := text[i]

		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch char {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}

		switch char {
		case '"':
			inString = true

		case '{', '[':
			stack = append(stack, i)

		case '}', ']':
			if len(stack) > 0 {
				startIdx := stack[len(stack)-1]
				startChar := text[startIdx]

				if (startChar == '{' && char == '}') || (startChar == '[' && char == ']') {
					match[startIdx] = i
					stack = stack[:len(stack)-1]
				} else {
					// Mismatched bracket indicates a malformed structure.
					// Clearing the stack is a robust way to handle this,
					// allowing the parser to recover and find subsequent valid blocks.
					stack = nil
				}
			}
		}
	}

	startIndices := make([]int, 0, len(match))
	for i := range match {
		startIndices = append(startIndices, i)
	}
	sort.Ints(startIndices)

	blocks := make([]string, 0, len(startIndices))
	for _, i := range startIndices {
		blocks = append(blocks, text[i:match[i]+1])
	}

	return blocks
}
