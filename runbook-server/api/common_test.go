package api

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
)

// newTestValidator mirrors the production validator's json-tag naming
// (internal/model/validation.go) so field names in messages match runtime.
func newTestValidator() *validator.Validate {
	v := validator.New()
	v.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
		if name == "-" {
			return ""
		}
		return name
	})
	return v
}

func TestFormatValidationError_Min(t *testing.T) {
	v := newTestValidator()

	// Slice min (the #34598 case: an empty task list -> "tasks failed
	// validation: min"), string min, and a pointer field — go-playground's
	// FieldError.Kind() dereferences pointers, so *string reports Kind()==string
	// and needs no special handling.
	type doc struct {
		Tasks []string `json:"tasks" validate:"min=1"`
		Name  string   `json:"name"  validate:"min=3"`
		Alias *string  `json:"alias" validate:"omitempty,min=3"`
	}

	shortAlias := "ab"

	cases := []struct {
		name string
		in   doc
		want string
	}{
		{"empty slice spells out item count", doc{Tasks: []string{}, Name: "abc"}, "tasks must contain at least 1 item"},
		{"short string spells out characters", doc{Tasks: []string{"t"}, Name: "ab"}, "name must be at least 3 characters long"},
		{"pointer field is dereferenced", doc{Tasks: []string{"t"}, Name: "abc", Alias: &shortAlias}, "alias must be at least 3 characters long"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := v.Struct(tc.in)
			if err == nil {
				t.Fatalf("expected a validation error, got nil")
			}
			if got := formatValidationError(err); got != tc.want {
				t.Errorf("formatValidationError() = %q, want %q", got, tc.want)
			}
		})
	}
}
