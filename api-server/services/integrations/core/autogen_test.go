package core

import (
	"errors"
	"testing"

	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAutogenRegistry_RegisterAndGet(t *testing.T) {
	const name = "test_autogen_registry_register_and_get"
	called := false
	want := AutoGenResult{Options: []AutoGenOption{{Label: "a", Value: "a"}}}
	RegisterAutoGenHandler(name, func(_ *security.RequestContext, _ map[string]any) (AutoGenResult, error) {
		called = true
		return want, nil
	})

	h, ok := GetAutoGenHandler(name)
	require.True(t, ok, "handler should be registered")
	got, err := h(nil, map[string]any{})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, want, got)
}

func TestAutogenRegistry_LookupIsCaseInsensitive(t *testing.T) {
	const name = "TestAutogen_Case_Sensitivity"
	RegisterAutoGenHandler(name, func(_ *security.RequestContext, _ map[string]any) (AutoGenResult, error) {
		return AutoGenResult{}, nil
	})

	for _, lookup := range []string{name, "testautogen_case_sensitivity", "TESTAUTOGEN_CASE_SENSITIVITY"} {
		_, ok := GetAutoGenHandler(lookup)
		assert.True(t, ok, "lookup %q should resolve", lookup)
	}
}

func TestAutogenRegistry_UnknownHandlerReturnsNotFound(t *testing.T) {
	h, ok := GetAutoGenHandler("nonexistent_handler_xyz")
	assert.False(t, ok)
	assert.Nil(t, h)
}

func TestAutogenRegistry_NilOrEmptyRegistrationIgnored(t *testing.T) {
	RegisterAutoGenHandler("", func(_ *security.RequestContext, _ map[string]any) (AutoGenResult, error) {
		return AutoGenResult{}, nil
	})
	_, ok := GetAutoGenHandler("")
	assert.False(t, ok, "empty name should not register")

	RegisterAutoGenHandler("test_autogen_nil_handler", nil)
	_, ok = GetAutoGenHandler("test_autogen_nil_handler")
	assert.False(t, ok, "nil handler should not register")
}

func TestAutogenRegistry_HandlerErrorsArePropagated(t *testing.T) {
	const name = "test_autogen_error_propagation"
	want := errors.New("boom")
	RegisterAutoGenHandler(name, func(_ *security.RequestContext, _ map[string]any) (AutoGenResult, error) {
		return AutoGenResult{}, want
	})

	h, ok := GetAutoGenHandler(name)
	require.True(t, ok)
	_, err := h(nil, nil)
	assert.ErrorIs(t, err, want)
}
