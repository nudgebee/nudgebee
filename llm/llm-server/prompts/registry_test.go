package prompts

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPromptMappingsNoLeadingTrailingSpaces(t *testing.T) {
	// Verify that all prompt mappings don't have leading or trailing spaces
	// in their filename strings, which would cause file resolution to fail

	for constant, mapping := range promptMapping {
		trimmed := strings.TrimSpace(mapping.name)
		assert.Equal(t, trimmed, mapping.name,
			"Mapping %s has leading or trailing spaces in filename: '%s'",
			constant, mapping.name)

		// Also ensure the name doesn't have any other whitespace issues
		assert.NotContains(t, mapping.name, "  ",
			"Mapping %s has double spaces: '%s'", constant, mapping.name)
	}
}

func TestPromptMappingsFileNamesValid(t *testing.T) {
	// Verify that all prompt names are valid filenames (no path separators, etc.)
	invalidChars := []string{"/", "\\", "..", "\n", "\t"}

	for constant, mapping := range promptMapping {
		for _, char := range invalidChars {
			assert.NotContains(t, mapping.name, char,
				"Mapping %s contains invalid character '%s' in filename: '%s'",
				constant, char, mapping.name)
		}
	}
}
