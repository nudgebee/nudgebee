package common

import (
	"regexp"

	"github.com/go-playground/validator/v10"
)

var validate *validator.Validate = validator.New(validator.WithRequiredStructEnabled())

// Compiled once at package init instead of on every function call.
var (
	k8sAccountNameRegex = regexp.MustCompile(`^[a-zA-Z0-9]([-a-zA-Z0-9\s_-]*[a-zA-Z0-9\s_-])?$`)
	userEmailRegex      = regexp.MustCompile(`^(([^<>()[\]\\.,;:\s@"]+(\.[^<>()[\]\\.,;:\s@"]+)*)|(".+"))@((\[[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}])|(([a-zA-Z\-0-9]+\.)+[a-zA-Z]{2,}))$`)
	// dnsLabelRegex matches a valid Kubernetes DNS label (RFC 1123).
	// Non-capturing group used; submatch is not needed.
	dnsLabelRegex = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$`)
)

func ValidateStruct(s interface{}) error {
	return validate.Struct(s)
}

func IsValidK8sDNSLabel(s string) bool {
	return dnsLabelRegex.MatchString(s)
}

func IsValidK8sAccountName(name string) bool {
	if len(name) > 40 {
		return false
	}

	return k8sAccountNameRegex.MatchString(name)
}

func IsValidUserEmail(name string) bool {
	return userEmailRegex.MatchString(name)
}
