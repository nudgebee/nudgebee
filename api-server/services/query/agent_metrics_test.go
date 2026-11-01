package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAsStringSlice_JSONArray(t *testing.T) {
	// Mimics encoding/json decoding of ["resource-a","resource-b"].
	got, ok := asStringSlice([]any{"resource-a", "resource-b"})
	require.True(t, ok)
	assert.Equal(t, []string{"resource-a", "resource-b"}, got)
}

func TestAsStringSlice_NativeStringSlice(t *testing.T) {
	got, ok := asStringSlice([]string{"m1", "m2"})
	require.True(t, ok)
	assert.Equal(t, []string{"m1", "m2"}, got)
}

func TestAsStringSlice_Empty(t *testing.T) {
	got, ok := asStringSlice([]any{})
	require.True(t, ok)
	assert.Equal(t, []string{}, got)
}

func TestAsStringSlice_RejectsNonStrings(t *testing.T) {
	_, ok := asStringSlice([]any{"ok", 42})
	assert.False(t, ok)
}

func TestAsStringSlice_RejectsWrongType(t *testing.T) {
	_, ok := asStringSlice("not-a-slice")
	assert.False(t, ok)
	_, ok = asStringSlice(nil)
	assert.False(t, ok)
}
