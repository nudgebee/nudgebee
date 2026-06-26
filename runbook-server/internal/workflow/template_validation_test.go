package workflow

import (
	"testing"

	"nudgebee/runbook/internal/model"

	"github.com/stretchr/testify/assert"
)

func TestValidateTemplateSyntax_TzZones(t *testing.T) {
	t.Run("valid_zone_passes", func(t *testing.T) {
		tasks := []model.Task{{
			ID:     "t1",
			Params: map[string]any{"message": `Now: {{ now() | tz("Asia/Kolkata") | strftime("%H:%M") }}`},
		}}
		assert.NoError(t, ValidateTemplateSyntax(tasks))
	})

	t.Run("empty_zone_is_utc_and_passes", func(t *testing.T) {
		tasks := []model.Task{{
			ID:     "t1",
			Params: map[string]any{"message": `{{ now() | tz("") }}`},
		}}
		assert.NoError(t, ValidateTemplateSyntax(tasks))
	})

	t.Run("invalid_zone_fails_at_save", func(t *testing.T) {
		tasks := []model.Task{{
			ID:     "t1",
			Params: map[string]any{"message": `{{ now() | tz("Asia/Kolkta") }}`},
		}}
		err := ValidateTemplateSyntax(tasks)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid timezone")
	})

	t.Run("invalid_zone_in_if_condition_fails", func(t *testing.T) {
		tasks := []model.Task{{
			ID: "t1",
			If: `{{ (now() | tz("Not/AZone") | strftime("%H") | int) >= 9 }}`,
		}}
		assert.Error(t, ValidateTemplateSyntax(tasks))
	})
}

func TestLintTemplates_DialectMixups(t *testing.T) {
	t.Run("date_format_with_strftime_codes_warns", func(t *testing.T) {
		tasks := []model.Task{{
			ID:     "t1",
			Params: map[string]any{"message": `{{ now() | date_format("%Y-%m-%d") }}`},
		}}
		warnings := LintTemplates(tasks)
		assert.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "date_format")
	})

	t.Run("strftime_with_go_layout_warns", func(t *testing.T) {
		tasks := []model.Task{{
			ID:     "t1",
			Params: map[string]any{"message": `{{ now() | strftime("2006-01-02") }}`},
		}}
		warnings := LintTemplates(tasks)
		assert.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "strftime")
	})

	t.Run("correct_usage_no_warnings", func(t *testing.T) {
		tasks := []model.Task{{
			ID: "t1",
			Params: map[string]any{
				"a": `{{ now() | strftime("%Y-%m-%d %H:%M") }}`,
				"b": `{{ now() | date_format("2006-01-02 15:04") }}`,
			},
		}}
		assert.Empty(t, LintTemplates(tasks))
	})
}
