package agents

import (
	"regexp"
	"strings"
)

func updateMarkDown(content string) string {
	content = strings.ReplaceAll(content, "**", "*")
	content = strings.ReplaceAll(content, "####", "*")
	content = strings.ReplaceAll(content, "###", "*")
	content = strings.ReplaceAll(content, "##", "*")
	content = strings.ReplaceAll(content, "#", "*")
	return content
}

var jsonObjectRegex = regexp.MustCompile(`\{\s*("[^"]*"\s*:\s*("[^"]*"\s*|\s*\{\s*[^\}]*\s*\}\s*),?\s*)*\s*\}(,?\s*)`)

func ExtractJsonObjectOrDefault(q string, def string) string {
	jsonString := jsonObjectRegex.FindString(q)
	if jsonString != "" {
		return jsonString
	}
	return def
}

var (
	// Regex to clean up multiple consecutive newlines
	multipleNewlinesRegex = regexp.MustCompile(`\n{3,}`)
	// Regex to find opening XML tags: <tagname> or <tagname attr="value">
	openingXMLTagRegex = regexp.MustCompile(`<([a-zA-Z_][a-zA-Z0-9_-]*)[^>]*>`)
)

// RemoveXMLTags removes ANY XML tags from the content that LLMs sometimes add for reasoning
// This removes all XML-like tags (opening and closing pairs) along with their content
// Common use case: cleaning up LLM responses that include <thinking>, <analysis>, etc.
func RemoveXMLTags(content string) string {
	// Keep removing tags until none are found (handles nested cases)
	prevContent := ""
	cleaned := content
	maxIterations := 20 // Prevent infinite loops
	iteration := 0

	for cleaned != prevContent && iteration < maxIterations {
		prevContent = cleaned
		// Try to find and remove any opening/closing XML tag pairs
		cleaned = removeFirstXMLTagPair(cleaned)
		iteration++
	}

	// Clean up any extra whitespace that might be left after removing tags
	cleaned = strings.TrimSpace(cleaned)

	// Remove multiple consecutive newlines (more than 2)
	cleaned = multipleNewlinesRegex.ReplaceAllString(cleaned, "\n\n")

	return cleaned
}

// removeFirstXMLTagPair finds and removes the first matching XML tag pair
// This approach handles any tag name dynamically without needing a predefined list
func removeFirstXMLTagPair(content string) string {
	// Find all opening tags
	matches := openingXMLTagRegex.FindAllStringSubmatchIndex(content, -1)

	if len(matches) == 0 {
		return content
	}

	// Try each opening tag to find a matching closing tag
	for _, match := range matches {
		// match[0] = start of entire match, match[1] = end of entire match
		// match[2] = start of captured group (tag name), match[3] = end of captured group
		tagName := content[match[2]:match[3]]
		openingTagStart := match[0]
		openingTagEnd := match[1]

		// Look for the matching closing tag after the opening tag
		closingTag := "</" + tagName + ">"
		closingTagIndex := strings.Index(content[openingTagEnd:], closingTag)

		if closingTagIndex != -1 {
			// Found a matching closing tag
			closingTagAbsoluteStart := openingTagEnd + closingTagIndex
			closingTagAbsoluteEnd := closingTagAbsoluteStart + len(closingTag)

			// Use strings.Builder for efficient string concatenation
			var builder strings.Builder
			builder.Grow(len(content) - (closingTagAbsoluteEnd - openingTagStart))
			builder.WriteString(content[:openingTagStart])
			builder.WriteString(content[closingTagAbsoluteEnd:])
			return builder.String()
		}
	}

	// No matching closing tag found for any opening tag
	return content
}
