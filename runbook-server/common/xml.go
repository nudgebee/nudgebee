package common

import (
	"fmt"
	"regexp"
	"strings"
)

// XmlExtractCDATA extracts content from CDATA tags using regex, handling incomplete strings
func XmlExtractCDATA(data string) string {
	// Handle empty or very short strings
	if len(data) == 0 {
		return data
	}

	// Trim whitespace to handle cases like '\n        <![CDATA[\n# ...'
	trimmed := strings.TrimSpace(data)

	// First try complete CDATA pattern with regex (most robust) - handles multiline
	re := regexp.MustCompile(`(?s)<!\[CDATA\[(.*?)\]\]>`)
	if matches := re.FindStringSubmatch(trimmed); len(matches) > 1 {
		return matches[1]
	}

	// Handle complete CDATA manually (fallback for edge cases)
	if strings.HasPrefix(trimmed, "<![CDATA[") && strings.HasSuffix(trimmed, "]]>") {
		if len(trimmed) >= 12 { // Minimum length for "<![CDATA[]]>"
			return trimmed[9 : len(trimmed)-3]
		}
	}

	// Handle incomplete CDATA (missing closing tag) - common in streaming
	if strings.HasPrefix(trimmed, "<![CDATA[") {
		if len(trimmed) > 9 {
			content := trimmed[9:]
			// Remove leading newline if present after CDATA opening
			content = strings.TrimPrefix(content, "\n")
			content = strings.TrimSuffix(content, "] ]>")
			content = strings.TrimSuffix(content, "]>")
			return content
		}
		return ""
	}

	// Handle malformed CDATA variants (missing opening chars)
	if strings.HasPrefix(trimmed, "CDATA[") && strings.HasSuffix(trimmed, "]]>") {
		if len(trimmed) >= 9 { // Minimum length for "CDATA[]]>"
			return trimmed[6 : len(trimmed)-3]
		}
	}

	if strings.HasPrefix(trimmed, "CDATA[") {
		if len(trimmed) > 6 {
			content := trimmed[6:]
			content = strings.TrimSuffix(content, "] ]>")
			content = strings.TrimSuffix(content, "]>")
			return content
		}
		return ""
	}

	// Return original data if no CDATA pattern found
	return data
}

// XmlExtractTagContent extracts the content from within a specified XML tag from a given string.
// It also handles CDATA sections within the tag.
func XmlExtractTagContent(output, tagName string) string {
	startTag := fmt.Sprintf("<%s>", tagName)
	endTag := fmt.Sprintf("</%s>", tagName)

	startIndex := strings.Index(output, startTag)
	endIndex := strings.Index(output, endTag)

	data := ""
	// Only extract content if both start and end tags are present and in the correct order.
	if startIndex != -1 && endIndex != -1 && endIndex > startIndex {
		data = strings.TrimSpace(output[startIndex+len(startTag) : endIndex])
		data = XmlExtractCDATA(data)
		data = strings.ReplaceAll(data, "&lt;", "<")
		data = strings.ReplaceAll(data, "&gt;", ">")
		data = strings.ReplaceAll(data, "&quot;", "\"")
		data = strings.ReplaceAll(data, "&amp;", "&")
		data = strings.ReplaceAll(data, "&apos;", "'")
		data = strings.ReplaceAll(data, "&nbsp;", " ")
	}

	return data
}

// XmlSanitize attempts to fix truncated XML by closing the specified root tag if it's open.
// It also attempts to close the last opened inner tag before closing the root tag.
func XmlSanitize(output, rootTag string) string {
	output = strings.TrimSpace(output)
	if strings.Contains(output, "<"+rootTag+">") && !strings.Contains(output, "</"+rootTag+">") {
		// Attempt to close the last opened tag.
		lastOpen := strings.LastIndex(output, "<")
		lastClose := strings.LastIndex(output, ">")
		if lastOpen > lastClose {
			tagName := output[lastOpen+1:]
			// handle attributes
			if strings.Contains(tagName, " ") {
				tagName = tagName[:strings.Index(tagName, " ")]
			}
			if !strings.HasPrefix(tagName, "/") {
				output += "</" + tagName + ">"
			}
		}
		output += "</" + rootTag + ">"
	}
	return output
}

// XmlRegexExtract extracts the first match of a regex pattern from a string.
func XmlRegexExtract(content string, re *regexp.Regexp) string {
	matches := re.FindStringSubmatch(content)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}
