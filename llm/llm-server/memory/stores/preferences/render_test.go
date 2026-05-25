package preferences

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRender_EmptyReturnsEmpty(t *testing.T) {
	assert.Equal(t, "", Render(nil))
	assert.Equal(t, "", Render([]Preference{}))
}

func TestRender_ScalarValues(t *testing.T) {
	prefs := []Preference{
		{Key: KeyTimezone, Value: "America/New_York"},
		{Key: KeyPreferredCloud, Value: "aws"},
		{Key: KeyConfirmDestructive, Value: true},
	}
	got := Render(prefs)

	assert.True(t, strings.HasPrefix(got, "<user_preferences>"))
	assert.True(t, strings.HasSuffix(got, "</user_preferences>"))
	assert.Contains(t, got, "timezone: America/New_York")
	assert.Contains(t, got, "preferred_cloud: aws")
	assert.Contains(t, got, "confirm_destructive: true")
}

func TestRender_SliceValues(t *testing.T) {
	prefs := []Preference{
		{Key: "preferred_clusters", Value: []any{"prod-use1", "stage-use1"}},
	}
	got := Render(prefs)
	assert.Contains(t, got, "preferred_clusters: prod-use1, stage-use1")
}

func TestRender_NilValueOmittedValue(t *testing.T) {
	// A preference with nil value still renders its key (rare but harmless).
	prefs := []Preference{{Key: "foo", Value: nil}}
	got := Render(prefs)
	assert.Contains(t, got, "foo: ")
}
