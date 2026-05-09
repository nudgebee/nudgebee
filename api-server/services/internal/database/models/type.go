package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
)

type Json struct {
	array   []any
	object  any
	isArray bool
}

func (a Json) IsArray() bool {
	return a.isArray
}

func (a Json) IsObject() bool {
	return !a.isArray
}

func (a Json) Array() []any {
	return a.array
}

func (a Json) Object() any {
	return a.object
}

func (a Json) Value() (driver.Value, error) {
	data, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

func (a *Json) Scan(value interface{}) (err error) {
	if value != nil {
		b, ok := value.([]uint8)
		if !ok {
			return errors.New("type assertion to []uint8 failed")
		}
		a.isArray = b[0] == '['
		if a.isArray {
			err = json.Unmarshal(b, &a.array)
			if err != nil {
				fmt.Println("Error unmarshalling json", err, b[0])
			}
		} else {
			err = json.Unmarshal(b, &a.object)
			if err != nil {
				fmt.Println("Error unmarshalling json", err, b[0])
			}
		}

		return err
	}
	return
}

func (a Json) MarshalJSON() ([]byte, error) {
	if a.isArray {
		return json.Marshal(a.array)
	}
	return json.Marshal(a.object)
}

func (a *Json) UnmarshalJSON(data []byte) error {
	if data[0] == '[' {
		a.isArray = true
		return json.Unmarshal(data, &a.array)
	}
	a.isArray = false
	return json.Unmarshal(data, &a.object)
}

func NewJsonArray(array []any) Json {
	return Json{array: array, isArray: true}
}
func NewJsonObject(object any) Json {
	return Json{object: object, isArray: false}
}
