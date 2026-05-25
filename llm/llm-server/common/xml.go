package common

import (
	"fmt"
	"regexp"
	"strings"
)

var xmlMismatchedTagRe = regexp.MustCompile(`element <(.+?)> closed by </(.+?)>`)

// knownEntities is the set of XML named entities that must not be re-escaped.
var knownEntities = map[string]bool{
	"amp": true, "lt": true, "gt": true,
	"apos": true, "quot": true, "nbsp": true,
}

// XmlEscapeAmpersands replaces bare & characters in XML content with &amp;,
// leaving already-encoded entities (named, numeric &amp;#N; and hex &amp;#xN;)
// untouched. This fixes a common LLM output issue where natural language
// (e.g. "foo & bar") appears unescaped inside XML tags.
func XmlEscapeAmpersands(content string) string {
	if !strings.Contains(content, "&") {
		return content
	}
	var b strings.Builder
	b.Grow(len(content))
	for i := 0; i < len(content); {
		if content[i] != '&' {
			b.WriteByte(content[i])
			i++
			continue
		}
		// Peek ahead to see if this looks like a valid entity reference.
		isValid, length := isValidEntityRef(content[i+1:])
		if isValid {
			b.WriteString(content[i : i+1+length]) // copy '&' + entity verbatim
			i += 1 + length
		} else {
			b.WriteString("&amp;")
			i++
		}
	}
	return b.String()
}

// isValidEntityRef returns whether the string starting right after '&' forms a
// valid XML entity reference (named or numeric), and the length of that entity
// (excluding the leading '&') so the caller can skip past it.
func isValidEntityRef(after string) (bool, int) {
	if len(after) == 0 {
		return false, 0
	}
	// Numeric entities: &#N; or &#xN;
	if after[0] == '#' {
		end := strings.IndexByte(after, ';')
		if end < 2 {
			return false, 0
		}
		digits := after[1:end]
		if len(digits) == 0 {
			return false, 0
		}
		if digits[0] == 'x' || digits[0] == 'X' {
			digits = digits[1:]
		}
		if len(digits) == 0 {
			return false, 0
		}
		for _, c := range digits {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
				return false, 0
			}
		}
		return true, end + 1 // +1 for the semicolon
	}
	// Named entities: &name;
	end := strings.IndexByte(after, ';')
	if end < 1 {
		return false, 0
	}
	name := after[:end]
	if knownEntities[name] {
		return true, end + 1 // +1 for the semicolon
	}
	return false, 0
}

// XmlFixMismatchedTag parses a Go xml.Unmarshal error string to detect
// "element <X> closed by </Y>" mismatches and replaces every occurrence of
// </Y> with </X> in content. Returns the fixed content and true if a fix was applied.
func XmlFixMismatchedTag(content, xmlErr string) (string, bool) {
	matches := xmlMismatchedTagRe.FindStringSubmatch(xmlErr)
	if len(matches) < 3 {
		return content, false
	}
	expected := matches[1] // tag that was opened
	actual := matches[2]   // wrong closing tag the LLM used
	if expected == actual {
		return content, false
	}
	fixed := strings.ReplaceAll(content, "</"+actual+">", "</"+expected+">")
	return fixed, fixed != content
}

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

// XmlExtractCDATAOrDefault extracts content from CDATA tags or returns the default value if none found.
func XmlExtractCDATAOrDefault(data string, def string) string {
	extracted := XmlExtractCDATA(data)
	if extracted == data {
		return def
	}
	return extracted
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

	// Clean up common misspelled closing tags generated by LLMs
	// Example: </critque_response> -> </critique_response>
	if strings.Contains(output, "</") {
		// Heuristic: if a closing tag looks like the root tag but is slightly different
		// we replace it.
		// For now, let's handle specific known typos
		typos := map[string]string{
			"</critque_response>": "</critique_response>",
			"</thought_acton>":    "</thought_action>",
			"</finalAnswer>":      "</final_answer>",
		}
		for typo, correction := range typos {
			output = strings.ReplaceAll(output, typo, correction)
		}
	}

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
