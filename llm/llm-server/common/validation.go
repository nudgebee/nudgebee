package common

import (
	"regexp"

	validator "github.com/go-playground/validator/v10"
)

var validate *validator.Validate = validator.New()

func ValidateStruct(s any) error {
	return validate.Struct(s)
}

//tobe used for agent , tool and function names

var NameRegex = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{2,49}$`)

func IsValidName(name string) bool {
	return NameRegex.MatchString(name)
}

// KBNameRegex is a relaxed regex for knowledgebase/skill names.
// Allows letters, digits, underscores, spaces, hyphens, and colons; 3-100 chars total.
var KBNameRegex = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_ \-:]{2,99}$`)

func IsValidKBName(name string) bool {
	return KBNameRegex.MatchString(name)
}

// KubernetesNameRegex follows DNS-1123 label standard: lowercase alphanumeric, '-', start/end with alphanumeric
// This strictly prevents SQL injection characters like space, quote, semicolon, etc.
var KubernetesNameRegex = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func IsValidKubernetesName(name string) bool {
	return KubernetesNameRegex.MatchString(name)
}

// HasNonEmptyValue checks if a value is non-nil and non-empty
// Useful for validating map values from parsed labels/configs
func HasNonEmptyValue(value any) bool {
	if value == nil {
		return false
	}

	switch v := value.(type) {
	case string:
		return v != ""
	case []any:
		return len(v) > 0
	case []string:
		return len(v) > 0
	default:
		// For other types, consider non-nil as having a value
		return true
	}
}
