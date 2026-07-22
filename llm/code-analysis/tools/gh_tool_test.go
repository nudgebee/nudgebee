package tools

import (
	"reflect"
	"testing"
)

func TestRewritePREditToRESTAPI(t *testing.T) {
	// repoDir/token are only consulted when --repo is absent (which triggers a
	// `gh repo view` exec). Every case here passes --repo explicitly or expects
	// no rewrite, so resolution is never reached.
	tests := []struct {
		name   string
		args   []string
		want   []string
		wantOK bool
	}{
		{
			name:   "title and body with explicit repo",
			args:   []string{"pr", "edit", "32688", "--repo", "owner/repo", "--title", "new title", "--body", "new body"},
			want:   []string{"api", "--method", "PATCH", "repos/owner/repo/pulls/32688", "-f", "title=new title", "-f", "body=new body"},
			wantOK: true,
		},
		{
			name:   "body only",
			args:   []string{"pr", "edit", "12", "-R", "o/r", "--body", "Fixes #26787"},
			want:   []string{"api", "--method", "PATCH", "repos/o/r/pulls/12", "-f", "body=Fixes #26787"},
			wantOK: true,
		},
		{
			name:   "title only",
			args:   []string{"pr", "edit", "5", "--repo", "o/r", "-t", "feat: thing"},
			want:   []string{"api", "--method", "PATCH", "repos/o/r/pulls/5", "-f", "title=feat: thing"},
			wantOK: true,
		},
		{
			name:   "pr URL ref",
			args:   []string{"pr", "edit", "https://github.com/owner/repo/pull/99", "--repo", "owner/repo", "--body", "x"},
			want:   []string{"api", "--method", "PATCH", "repos/owner/repo/pulls/99", "-f", "body=x"},
			wantOK: true,
		},
		{
			name:   "body preserves newlines",
			args:   []string{"pr", "edit", "1", "--repo", "o/r", "--body", "line1\nline2"},
			want:   []string{"api", "--method", "PATCH", "repos/o/r/pulls/1", "-f", "body=line1\nline2"},
			wantOK: true,
		},
		// --- pass-through cases (must NOT rewrite) ---
		{
			name:   "label edit is left to gh",
			args:   []string{"pr", "edit", "1", "--repo", "o/r", "--add-label", "bug"},
			wantOK: false,
		},
		{
			name:   "title plus label is left to gh (label unsupported via this path)",
			args:   []string{"pr", "edit", "1", "--repo", "o/r", "--title", "t", "--add-label", "bug"},
			wantOK: false,
		},
		{
			name:   "no mutating flags",
			args:   []string{"pr", "edit", "1", "--repo", "o/r"},
			wantOK: false,
		},
		{
			name:   "branch ref instead of number",
			args:   []string{"pr", "edit", "my-branch", "--repo", "o/r", "--body", "x"},
			wantOK: false,
		},
		{
			name:   "not a pr edit command",
			args:   []string{"pr", "view", "1"},
			wantOK: false,
		},
		{
			name:   "issue edit untouched",
			args:   []string{"issue", "edit", "1", "--body", "x"},
			wantOK: false,
		},
		{
			name:   "dangling --title value",
			args:   []string{"pr", "edit", "1", "--repo", "o/r", "--title"},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := rewritePREditToRESTAPI(tt.args, "", "")
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (got args %v)", ok, tt.wantOK, got)
			}
			if tt.wantOK && !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("args mismatch\n got: %v\nwant: %v", got, tt.want)
			}
		})
	}
}

func TestExtractPRNumber(t *testing.T) {
	cases := map[string]string{
		"32688":                                 "32688",
		"https://github.com/owner/repo/pull/99": "99",
		"https://github.com/owner/repo/pull/99/files": "99",
		"my-branch": "",
		"":          "",
		// Branch names that merely contain "/pull/" must NOT be read as a PR
		// number — they lack the owner/repo segments a real PR URL has.
		"feature/pull/3":  "",
		"fix/pull/42-bug": "",
		"12a":             "",
	}
	for in, want := range cases {
		if got := extractPRNumber(in); got != want {
			t.Errorf("extractPRNumber(%q) = %q, want %q", in, got, want)
		}
	}
}
