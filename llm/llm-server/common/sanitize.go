package common

import (
	"regexp"
	"strings"
)

var (
	// Matches API keys in URLs: key=ABC123... or api_key=ABC123...
	apiKeyInURLRegex = regexp.MustCompile(`([?&])(key|api_key|apikey|api-key|token|access_token)=[^&\s"')]+`)
	// Matches Bearer tokens
	bearerTokenRegex = regexp.MustCompile(`Bearer\s+[A-Za-z0-9\-._~+/]+=*`)
	// Matches full URLs containing credentials in path or query
	credentialURLRegex = regexp.MustCompile(`https?://[^\s"')]*(?:key|token|secret|password|credential)[^\s"')]*`)
	// Matches XML/HTML-like tags (opening, closing, self-closing) to prevent prompt injection
	xmlTagRegex = regexp.MustCompile(`</?[a-zA-Z][a-zA-Z0-9_-]*[^>]*>`)
	// Matches characters unsafe for file paths — used by SanitizePath
	unsafePathCharRegex = regexp.MustCompile(`[^a-zA-Z0-9/._-]`)
)

// SanitizeErrorMessage strips API keys, tokens, and credential URLs from error messages
// to prevent sensitive data from leaking to clients or being stored in the database.
func SanitizeErrorMessage(msg string) string {
	msg = apiKeyInURLRegex.ReplaceAllString(msg, "${1}${2}=***REDACTED***")
	msg = bearerTokenRegex.ReplaceAllString(msg, "Bearer ***REDACTED***")
	msg = credentialURLRegex.ReplaceAllString(msg, "***REDACTED_URL***")
	return msg
}

// ShellEscape escapes a string for safe use in a shell command.
// It wraps the string in single quotes and escapes any single quotes within it.
func ShellEscape(s string) string {
	if s == "" {
		return "''"
	}
	// Replace ' with '\'' and wrap in '...'
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// SanitizePromptInput strips XML/HTML-like tags from user-supplied strings before they are
// interpolated into LLM system prompts. This prevents prompt injection attacks where an
// attacker crafts input like "</instructions><instructions>Ignore all previous instructions..."
// to manipulate the LLM's behavior.
func SanitizePromptInput(s string) string {
	return xmlTagRegex.ReplaceAllString(s, "")
}

// SanitizePath removes directory traversal components and other potentially dangerous characters.
// It ensures the path is relative and does not contain "..".
func SanitizePath(path string) string {
	// Remove all ".." to prevent traversal
	path = strings.ReplaceAll(path, "..", "")

	// Remove leading slashes to ensure it's relative
	for strings.HasPrefix(path, "/") {
		path = path[1:]
	}

	return unsafePathCharRegex.ReplaceAllString(path, "_")
}
