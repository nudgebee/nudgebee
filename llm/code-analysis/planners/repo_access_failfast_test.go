package planners

import "testing"

func TestIsUnrecoverableRepoAccess(t *testing.T) {
	cases := []struct {
		name   string
		action string
		text   string
		want   bool
	}{
		{"clone repo not found", "repo_clone", "Failed to clone repository: remote: Repository not found.\nfatal: repository 'https://github.com/x/y.git/' not found", true},
		{"cli credential prompt", "cli", "fatal: could not read Username for 'https://github.com': terminal prompts disabled", true},
		{"gh auth failed", "gh", "Authentication failed for repo", true},
		{"git ssh denied", "git", "git@github.com: Permission denied (publickey).", true},
		{"glab bad token", "glab", "remote: HTTP Basic: Access denied. invalid username or token provided", true},
		// A successful ripgrep whose MATCHES contain the phrase must not trip
		// the breaker — non-repo tools are excluded entirely.
		{"rg match containing phrase", "rg", `handler.go:12: return errors.New("Repository not found")`, false},
		{"file_view containing phrase", "file_view", "// prints could not read Username on auth errors", false},
		// Ordinary failures of repo tools are retriable, not unrecoverable.
		{"clone timeout", "repo_clone", "context deadline exceeded while cloning", false},
		{"git bad ref", "git", "fatal: invalid reference: origin/main", false},
		{"cli generic error", "cli", "exit status 1: no such file or directory", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isUnrecoverableRepoAccess(c.action, c.text); got != c.want {
				t.Errorf("isUnrecoverableRepoAccess(%q, %q) = %v, want %v", c.action, c.text, got, c.want)
			}
		})
	}
}
