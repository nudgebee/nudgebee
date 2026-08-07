package prompts

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPromptNamesAreCleanIdentifiers verifies registered prompt names are usable as
// both a filename stem and a DB prompt_name. The name is the single identifier now —
// there is no separate filename field that could carry stray whitespace.
func TestPromptNamesAreCleanIdentifiers(t *testing.T) {
	for name := range promptCategories {
		assert.Equal(t, strings.TrimSpace(name), name, "prompt %q has surrounding whitespace", name)
		for _, bad := range []string{"/", "\\", "..", "\n", "\t", " "} {
			assert.NotContains(t, name, bad, "prompt %q contains invalid character %q", name, bad)
		}
	}
}

// TestPromptCategoriesAreValid verifies every registered prompt names a real category
// directory, since resolution builds the path from it.
func TestPromptCategoriesAreValid(t *testing.T) {
	valid := map[PromptCategory]bool{
		CategoryAgents: true, CategoryPlanners: true,
		CategoryTools: true, CategoryUtilities: true, CategoryFragments: true,
	}
	for name, category := range promptCategories {
		assert.True(t, valid[category], "prompt %q has unknown category %q", name, category)
	}
}
