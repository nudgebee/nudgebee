package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRepoCloneErrorHint_Patterns pins the git failure classes surfaced by
// 2026-07-02 monitoring: every single repo_clone error persisted the same
// bare "Repository cloning failed" string with zero signal, because the
// git error only lived in the Error field (not Observation). The hint
// classes cover the six failure modes worth diagnosing; everything else
// falls through so raw git output stays authoritative.
func TestRepoCloneErrorHint_Patterns(t *testing.T) {
	cases := []struct {
		name     string
		rawError string
		want     string
	}{
		{
			name:     "authentication failed — retry with same creds would just fail again",
			rawError: "authentication failed for 'https://github.com/foo/bar.git/'",
			want:     "do NOT retry with the same credentials",
		},
		{
			name:     "invalid username or password variant",
			rawError: "fatal: Authentication failed: Invalid username or password",
			want:     "credentials are invalid",
		},
		{
			name:     "could not read username interactive prompt failure",
			rawError: "fatal: could not read Username for 'https://github.com': terminal prompts disabled",
			want:     "credentials are invalid",
		},
		{
			name:     "repository not found — steer to check the URL, not retry with same URL",
			rawError: "remote: Repository not found.\nfatal: repository 'https://github.com/foo/nonexistent.git/' not found",
			want:     "Report the exact URL back to the user",
		},
		{
			name:     "404 not found variant",
			rawError: "HTTP 404: Not Found (https://api.github.com/repos/foo/bar)",
			want:     "Report the exact URL",
		},
		{
			name:     "SSH publickey auth failed — steer to HTTPS form",
			rawError: "git@github.com: Permission denied (publickey).\nfatal: Could not read from remote repository.",
			want:     "retry with the HTTPS URL",
		},
		{
			name:     "DNS resolution failure",
			rawError: "fatal: unable to access 'https://github.com/foo/bar.git/': Could not resolve host: github.com",
			want:     "stop retrying",
		},
		{
			name:     "connection timeout",
			rawError: "fatal: unable to access 'https://github.com/foo/bar.git/': Connection timed out",
			want:     "stop retrying",
		},
		{
			name:     "branch does not exist on remote",
			rawError: "fatal: Remote branch nonexistent-branch not found in upstream origin",
			want:     "does not exist on the remote",
		},
		{
			name:     "couldn't find remote ref (branch missing)",
			rawError: "fatal: couldn't find remote ref refs/heads/nope",
			want:     "does not exist on the remote",
		},
		{
			name:     "disk full — infra issue, not model-recoverable",
			rawError: "fatal: fetch-pack: unable to write to disk: No space left on device",
			want:     "Not recoverable",
		},
		{
			name:     "unrecognized error — no hint, raw git output preserved",
			rawError: "fatal: some novel git error the model has never seen",
			want:     "",
		},
		{
			name:     "empty error — no hint",
			rawError: "",
			want:     "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := repoCloneErrorHint(c.rawError)
			if c.want == "" {
				assert.Empty(t, got, "expected no hint, got: %q", got)
				return
			}
			assert.NotEmpty(t, got)
			assert.Contains(t, got, c.want)
		})
	}
}

// TestRepoCloneErrorHint_CaseInsensitive locks in the case-insensitive
// matching — git error phrasing varies across versions and pagers.
func TestRepoCloneErrorHint_CaseInsensitive(t *testing.T) {
	assert.NotEmpty(t, repoCloneErrorHint("AUTHENTICATION FAILED for repo"))
	assert.NotEmpty(t, repoCloneErrorHint("Repository Not Found"))
}

// TestSanitizeGitError pins the credential-redaction contract added after
// Gemini review flagged that embedded tokens in clone URLs could leak into
// the LLM prompt / analytics column via git errors. Belt-and-suspenders:
// modern git usually redacts credentials, but not always (older versions,
// custom auth handlers, unusual URL shapes).
func TestSanitizeGitError(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "https token embedded in URL — must be redacted",
			in:   "fatal: unable to access 'https://ghp_veryLongToken1234567890abcdef@github.com/foo/bar.git/': ...",
			want: "fatal: unable to access 'https://<redacted>@github.com/foo/bar.git/': ...",
		},
		{
			name: "https user:pass in URL — must be redacted",
			in:   "fatal: unable to access 'https://alice:hunter2@gitlab.example.com/foo/bar.git/': Authentication failed",
			want: "fatal: unable to access 'https://<redacted>@gitlab.example.com/foo/bar.git/': Authentication failed",
		},
		{
			name: "http (plain) with creds — also redacted",
			in:   "fatal: unable to access 'http://bob:secret@internal-host/repo.git/'",
			want: "fatal: unable to access 'http://<redacted>@internal-host/repo.git/'",
		},
		{
			name: "no credentials — unchanged byte-for-byte",
			in:   "fatal: unable to access 'https://github.com/foo/bar.git/': Repository not found",
			want: "fatal: unable to access 'https://github.com/foo/bar.git/': Repository not found",
		},
		{
			name: "ssh git@ form — deliberately left untouched (git@ is a fixed username, not a secret)",
			in:   "git@github.com: Permission denied (publickey).",
			want: "git@github.com: Permission denied (publickey).",
		},
		{
			name: "multiple URLs in one error — both get sanitized",
			in:   "fetch from https://tok1@a.example/x.git failed; push to https://user:tok2@b.example/y.git also failed",
			want: "fetch from https://<redacted>@a.example/x.git failed; push to https://<redacted>@b.example/y.git also failed",
		},
		{
			name: "empty string — passthrough",
			in:   "",
			want: "",
		},
		{
			name: "no URL at all — passthrough",
			in:   "fatal: no space left on device",
			want: "fatal: no space left on device",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sanitizeGitError(c.in)
			assert.Equal(t, c.want, got)
			// The specific token bytes must not survive when they were in the input.
			for _, secret := range []string{"ghp_veryLongToken1234567890abcdef", "hunter2", "secret", "tok1", "tok2"} {
				if strings.Contains(c.in, secret) {
					assert.NotContains(t, got, secret,
						"sanitized output must not contain the embedded secret %q", secret)
				}
			}
		})
	}
}

// TestSanitizeGitError_Idempotent confirms running the sanitizer twice is
// safe — <redacted>@ is not itself a redaction target.
func TestSanitizeGitError_Idempotent(t *testing.T) {
	once := sanitizeGitError("fatal: unable to access 'https://ghp_x@github.com/foo/bar.git/'")
	twice := sanitizeGitError(once)
	assert.Equal(t, once, twice, "sanitizer must be idempotent")
}
