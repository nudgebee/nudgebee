package tools

import (
	"strings"
	"testing"
)

func TestGitHTTPSAuthEnv_WithToken(t *testing.T) {
	env := gitHTTPSAuthEnv("ghs_exampletoken")

	joined := strings.Join(env, "\n")
	for _, want := range []string{
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=url.https://x-access-token:ghs_exampletoken@github.com/.insteadOf",
		"GIT_CONFIG_VALUE_0=https://github.com/",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected env to contain %q, got:\n%s", want, joined)
		}
	}
}

func TestGitHTTPSAuthEnv_WithoutToken(t *testing.T) {
	env := gitHTTPSAuthEnv("")

	if len(env) != 1 || env[0] != "GIT_TERMINAL_PROMPT=0" {
		t.Fatalf("expected only GIT_TERMINAL_PROMPT=0 when no token, got: %v", env)
	}
	// The token-bearing insteadOf rewrite must not be emitted without a token.
	for _, e := range env {
		if strings.Contains(e, "insteadOf") {
			t.Fatalf("did not expect insteadOf rewrite without a token, got: %v", env)
		}
	}
}

func TestCommandInvokesGit(t *testing.T) {
	cases := map[string]bool{
		// Direct invocations.
		"git clone https://github.com/o/r .": true,
		"git fetch origin":                   true,
		"  git status":                       true,
		// Chained invocations must still authenticate.
		"cd foo && git status":             true,
		"mkdir bar && cd bar && git clone": true,
		"true; git fetch":                  true,
		"echo x | git hash-object --stdin": true,
		// Not a git invocation.
		"gh pr list":                         false,
		"ls -la":                             false,
		"gitfoo":                             false,
		"echo git clone":                     false,
		"this is a description about git":    false,
		"how to use git for version control": false,
	}
	for cmd, want := range cases {
		if got := commandInvokesGit(cmd); got != want {
			t.Errorf("commandInvokesGit(%q) = %v, want %v", cmd, got, want)
		}
	}
}
