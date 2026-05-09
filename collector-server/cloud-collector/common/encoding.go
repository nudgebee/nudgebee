package common

import (
	"errors"
	"reflect"
	"time"

	"github.com/mitchellh/mapstructure"
	"gopkg.in/yaml.v3"

	jsoniter "github.com/json-iterator/go"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

func stringToTimeHookFunc() mapstructure.DecodeHookFunc {
	return func(
		f reflect.Type,
		t reflect.Type,
		data interface{}) (interface{}, error) {
		if f.Kind() != reflect.String {
			return data, nil
		}
		if t != reflect.TypeOf(time.Time{}) {
			return data, nil
		}

		data1, ok := data.(string)
		if !ok {
			return data, nil
		}
		if data1 == "" {
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
				return parsedTime, nil
			}
		}

		return nil, err
	}
}

func UnmarshalMapToStruct(m map[string]any, s interface{}) error {
	// if s is not pointer then throw error
	if s == nil {
		return errors.New("DecodeMapToStruct: s is nil")
	}

	if reflect.TypeOf(s).Kind() != reflect.Pointer {
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
		panic(err)
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

func MarshalStructToMap(obj interface{}) (map[string]interface{}, error) {
	if obj == nil {
		return nil, errors.New("MarshalStructToMap: input object is nil")
	}

	val := reflect.ValueOf(obj)
	if val.Kind() == reflect.Pointer && val.IsNil() {
		return nil, errors.New("MarshalStructToMap: input object is a nil pointer")
	}

	// Ensure the underlying type is a struct if it's a pointer
	if val.Kind() == reflect.Pointer {
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return nil, errors.New("MarshalStructToMap: input object is not a struct or a pointer to a struct")
	}

	data, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
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
