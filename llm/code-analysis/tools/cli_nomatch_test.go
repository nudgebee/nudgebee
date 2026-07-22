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
	}
	for cmd, want := range cases {
		if got := isSearchNoMatch(cmd); got != want {
			t.Errorf("isSearchNoMatch(%q) = %v, want %v", cmd, got, want)
		}
	}
}
