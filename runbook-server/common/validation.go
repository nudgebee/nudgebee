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
