package common

import (
	validator "github.com/go-playground/validator/v10"
)

var validate *validator.Validate = validator.New()

func ValidateStruct(s interface{}) error {
	return validate.Struct(s)
}
