package common

import (
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/mitchellh/mapstructure"
	"gopkg.in/yaml.v3"
)

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

func DecodeMapToStruct(m map[string]any, s interface{}) error {
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

func EncodeToJsonSafe(data any) string {
	if data == nil {
		return ""
	}
	str, err := json.Marshal(data)
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

func StructToMap(obj interface{}) (map[string]interface{}, error) {
	objValue := reflect.ValueOf(obj)
	if objValue.Kind() != reflect.Struct {
		return nil, errors.New("DecodeMapToStruct: s is nil")
	}

	objType := objValue.Type()
	result := make(map[string]interface{})

	for i := 0; i < objType.NumField(); i++ {
		field := objType.Field(i)
		fieldValue := objValue.Field(i).Interface()
		result[field.Name] = fieldValue
	}

	return result, nil
}
