package git

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// credentialURLPattern matches the "user:pass@" userinfo of an HTTP(S) URL.
var credentialURLPattern = regexp.MustCompile(`(https?://)[^\s/@]+(?::[^\s/@]*)?@`)

// RedactURLCredentials strips embedded credentials from every HTTP(S) URL in text.
//
// Unlike StripURLUserinfo, which takes a single URL, this operates on arbitrary text:
// git quotes the push target back in its own output, so command output has to be
// scrubbed before it reaches a log line, an error message, or the model's context.
func RedactURLCredentials(text string) string {
	return credentialURLPattern.ReplaceAllString(text, "${1}")
}

// SplitRepoURL splits an http(s)://, ssh:// or scp-like git@host:path repository URL
// into its host and path. It returns empty strings when raw is shaped like none of
// those, so callers can treat "unparseable" and "not a repository URL" identically.
func SplitRepoURL(raw string) (host, path string) {
	if strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "ssh://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", ""
		}
		return u.Hostname(), u.Path
	}
	// scp-like: [user@]host:path — no scheme, and the colon separates host from path.
	if strings.Contains(raw, "://") {
		return "", ""
	}
	hostPart, pathPart, found := strings.Cut(raw, ":")
	if !found {
		return "", ""
	}
	if _, after, ok := strings.Cut(hostPart, "@"); ok {
		hostPart = after
	}
	return hostPart, pathPart
}

// ValidateRepoURL reports whether raw is a well-formed repository URL that is safe to
// hand to git.
//
// Repository URLs can originate in LLM output: the llm-server asks a model to extract
// one from free text, and a reasoning model may answer with prose instead. An
// unvalidated value of that shape reached a `git push` command line and was word-split
// by the shell, producing "fatal: invalid refspec 'text:'" (#35703). Validating here
// means a malformed value is refused at the boundary rather than executed.
func ValidateRepoURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("repository URL is empty")
	}
	if raw != strings.TrimSpace(raw) {
		return fmt.Errorf("repository URL has leading or trailing whitespace")
	}
	if strings.IndexFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return fmt.Errorf("repository URL contains whitespace or control characters")
	}
	// A leading dash would be read by git as an option rather than an argument.
	if strings.HasPrefix(raw, "-") {
		return fmt.Errorf("repository URL starts with %q", "-")
	}

	host, path := SplitRepoURL(raw)
	if host == "" {
		return fmt.Errorf("repository URL has no recognisable host")
	}
	if strings.HasPrefix(host, "-") {
		return fmt.Errorf("repository URL host starts with %q", "-")
	}

	segments := 0
	for _, segment := range strings.Split(strings.Trim(path, "/"), "/") {
		if segment != "" {
			segments++
		}
	}
	if segments < 2 {
		return fmt.Errorf("repository URL has no owner/repo path")
	}

	return nil
}

// ValidateBranchName reports whether branch is safe to pass to git as an argument.
//
// Branch names reach git from request payloads and LLM tool arguments. A name starting
// with "-" is read by git as an option rather than a ref — `git fetch origin
// --upload-pack=<cmd>` is the classic argument-injection shape — and whitespace or
// control characters mean the value was never a ref to begin with.
//
// An empty name is rejected here, but callers that treat "" as "no branch specified"
// (clone the default branch) must skip the check rather than fail: that is a supported
// input, not a malformed one.
func ValidateBranchName(branch string) error {
	if branch == "" {
		return fmt.Errorf("branch name is empty")
	}
	if branch != strings.TrimSpace(branch) {
		return fmt.Errorf("branch name has leading or trailing whitespace")
	}
	if strings.IndexFunc(branch, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return fmt.Errorf("branch name contains whitespace or control characters")
	}
	if strings.HasPrefix(branch, "-") {
		return fmt.Errorf("branch name starts with %q", "-")
	}
	return nil
}
