package tools

import "testing"

func TestIsSearchNoMatch(t *testing.T) {
	cases := map[string]bool{
		"rg --type go 'foo' .":             true,
		"grep -rn 'bar' src":               true,
		"rg 'x' . | grep 'y'":              true, // last stage is grep
		"egrep -i baz file":                true,
		"rg -l 'x' . | xargs cat":          false, // last stage is cat, not search
		"go build ./...":                   false,
		"ls -la":                           false,
		"cat file.go":                      false,
		"find . -name '*.go' | grep _test": true,
		"/usr/bin/grep -rn foo .":          true, // absolute path to binary
		"./bin/rg 'x' .":                   true, // relative path to binary
		"FOO=bar grep x file":              true, // leading env-var assignment
		"go build ./... | grep error":      true, // last stage is grep
		// Quoted patterns containing shell-metachar lookalikes must NOT shatter the
		// stage — these are common agent ops (conflict-marker / arrow-fn searches)
		// that legitimately exit 1 on no match.
		`grep -rn "<<<<<<< HEAD" .`:       true, // conflict markers, no pipe
		`grep -n "<<<<<<<" a.jsx b.tsx`:   true, // conflict markers, multiple files
		`git show abc file | grep "=> {"`: true, // last stage grep, arrow fn in pattern
		`grep foo bar > out.txt`:          true, // redirect target must not become the stage
	}
	for cmd, want := range cases {
		if got := isSearchNoMatch(cmd); got != want {
			t.Errorf("isSearchNoMatch(%q) = %v, want %v", cmd, got, want)
		}
	}
}
