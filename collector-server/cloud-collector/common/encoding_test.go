package common

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMapDecoding(t *testing.T) {
	t.Run("TestTimeDecoding", func(t *testing.T) {
		type TestStruct struct {
			CreatedAt time.Time `json:"created_at"`
		}

		// Test decoding of time
		t1 := TestStruct{}

		err := UnmarshalMapToStruct(map[string]any{
			"created_at": "2021-08-01T00:00:00Z",
		}, &t1)

		assert.Nil(t, err)

		assert.Equal(t, time.Date(2021, 8, 1, 0, 0, 0, 0, time.UTC), t1.CreatedAt)

	})

	t.Run("TestHasuraTimeDecoding", func(t *testing.T) {
		type TestStruct struct {
			CreatedAt time.Time `json:"created_at"`
		}

		// Test decoding of time
		t1 := TestStruct{}

		err := UnmarshalMapToStruct(map[string]any{
			"created_at": "2024-02-14T15:25:05.608513",
		}, &t1)

		assert.Nil(t, err)

		assert.Equal(t, time.Date(2024, 2, 14, 15, 25, 5, 608513000, time.UTC), t1.CreatedAt)

	})

	//2024-02-14T15:25:05.608513
	t.Run("TestStructToMap", func(t *testing.T) {
		type TestStruct struct {
			Field1 string
			Field2 int
			Field3 bool
		}

		testObj := TestStruct{
			Field1: "value1",
			Field2: 123,
			Field3: true,
		}

		expectedResult := map[string]interface{}{
			"Field1": "value1",
			"Field2": float64(123), // json.Unmarshal produces float64 for numbers
			"Field3": true,
		}

		result, err := MarshalStructToMap(testObj)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}

		if !reflect.DeepEqual(result, expectedResult) {
			t.Errorf("Unexpected result. Expected: %v, Got: %v", expectedResult, result)
		}
	})

	t.Run("TestStructToMapWithNil", func(t *testing.T) {
		type TestStruct struct {
			Field1 string
			Field2 int
			Field3 bool
		}
		var testObj *TestStruct
		_, err := MarshalStructToMap(testObj)
		if err == nil {
			t.Errorf("Expected error but got nil")
		}
	})
}
