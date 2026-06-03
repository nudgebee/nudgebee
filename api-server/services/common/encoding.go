package common

import (
	"errors"
	"log/slog"
	"reflect"
	"time"

	"github.com/fatih/structs"
	"github.com/mitchellh/mapstructure"
	"gopkg.in/yaml.v3"

	jsoniter "github.com/json-iterator/go"
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

func UnmarshalMapToStruct(m map[string]any, s any) error {
	// if s is not pointer then throw error
	if s == nil {
		return errors.New("DecodeMapToStruct: s is nil")
	}

	if reflect.TypeOf(s).Kind() != reflect.Ptr {
		return errors.New("DecodeMapToStruct: s is not a pointer")
	}

	decoderConfig := mapstructure.DecoderConfig{
		TagName:    "json",
		DecodeHook: stringToTimeHookFunc(),
		Result:     s,
	}

	decoder, err := mapstructure.NewDecoder(&decoderConfig)
	if err != nil {
		return err
	}

	return decoder.Decode(m)
}

func MarshalJsonSafeString(data any) string {
	if data == nil {
		return ""
	}
	str, err := json.Marshal(data)
	if err != nil {
		slog.Error("unable to marshal json", "error", err, "data", data)
	}

	return string(str)
}

func UnmarshalYamlToMap(data string) (map[string]any, error) {
	var result map[string]any
	err := yaml.Unmarshal([]byte(data), &result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func MarshalYaml(data any) ([]byte, error) {
	result, err := yaml.Marshal(data)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func MarshalStructToMap(obj any) (map[string]any, error) {
	if obj == nil {
		return nil, errors.New("MarshalStructToMap: obj is nil")
	}
	v := reflect.ValueOf(obj)
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil, errors.New("MarshalStructToMap: obj is nil")
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil, errors.New("MarshalStructToMap: obj is not a struct")
	}
	structObj := structs.New(obj)
	structObj.TagName = "json"
	return structObj.Map(), nil
}

func MarshalJson(obj any) ([]byte, error) {
	return json.Marshal(obj)
}

func UnmarshalJson(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func UnmarshalJsonString(data string, v any) error {
	if data == "" {
		return nil
	}
	return json.Unmarshal([]byte(data), v)
}
