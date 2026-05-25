package tools

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFindRepositoryDirectoryFromBase covers both the standard clone layout (.git as a
// directory) and the worktree layout (.git as a regular file pointing at the base repo).
//
// Reproduction of the production bug: when the workspace contains a worktree-cloned repo
// (which is what CloneOrReuseRepository in internal/git/client.go produces), .git is a
// gitfile, not a directory. The old isDirExists predicate rejected it and the helper
// fell back to the base directory, causing downstream "git" commands to fail with
// "fatal: not a git repository" inside callers like analyzeRecentCommits.
func TestFindRepositoryDirectoryFromBase(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T, base string) string // returns expected path
		wantBaseDir bool                                   // true if expected path == baseDir (no repo found)
	}{
		{
			name: "base dir itself has .git directory (standard clone)",
			setup: func(t *testing.T, base string) string {
				if err := os.MkdirAll(filepath.Join(base, ".git"), 0755); err != nil {
					t.Fatal(err)
				}
				return base
			},
		},
		{
			name: "base dir itself has .git file (worktree gitfile)",
			setup: func(t *testing.T, base string) string {
				if err := os.WriteFile(filepath.Join(base, ".git"), []byte("gitdir: /tmp/somewhere/.git/worktrees/x\n"), 0644); err != nil {
					t.Fatal(err)
				}
				return base
			},
		},
		{
			name: "subdirectory has .git directory (standard clone in subdir)",
			setup: func(t *testing.T, base string) string {
				repo := filepath.Join(base, "myrepo")
				if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
					t.Fatal(err)
				}
				return repo
			},
		},
		{
			name: "subdirectory has .git file (worktree-cloned repo in subdir)",
			setup: func(t *testing.T, base string) string {
				repo := filepath.Join(base, "nudgebee-infra")
				if err := os.MkdirAll(repo, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: /tmp/code-analysis/repos/nudgebee-infra/worktrees/x\n"), 0644); err != nil {
					t.Fatal(err)
				}
				return repo
			},
		},
		{
			name: "no .git anywhere falls back to baseDir",
			setup: func(t *testing.T, base string) string {
				if err := os.MkdirAll(filepath.Join(base, "src"), 0755); err != nil {
					t.Fatal(err)
				}
				return base
			},
			wantBaseDir: true,
		},
		{
			name: "first matching subdir wins when multiple repos present",
			setup: func(t *testing.T, base string) string {
				// os.ReadDir returns entries in lexical order, so "a-repo" should be picked.
				if err := os.MkdirAll(filepath.Join(base, "a-repo", ".git"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(base, "b-repo"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(base, "b-repo", ".git"), []byte("gitdir: ..."), 0644); err != nil {
					t.Fatal(err)
				}
				return filepath.Join(base, "a-repo")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			expected := tc.setup(t, base)
			rh := NewRepositoryHelper()
			got := rh.FindRepositoryDirectoryFromBase(base)
			if tc.wantBaseDir {
				if got != base {
					t.Errorf("expected fallback to baseDir %q, got %q", base, got)
				}
				return
			}
			if got != expected {
				t.Errorf("got %q, want %q", got, expected)
			}
		})
	}
}

// TestFindRepositoryDirectoryFromBase_NonexistentBase verifies that a missing base
// directory returns the base unchanged (callers are expected to handle the error).
func TestFindRepositoryDirectoryFromBase_NonexistentBase(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	rh := NewRepositoryHelper()
	got := rh.FindRepositoryDirectoryFromBase(missing)
	if got != missing {
		t.Errorf("got %q, want %q", got, missing)
	}
}
