package tools

import (
	"os"
	"path/filepath"
)

// RepositoryHelper provides shared repository discovery functionality
type RepositoryHelper struct{}

// NewRepositoryHelper creates a new repository helper
func NewRepositoryHelper() *RepositoryHelper {
	return &RepositoryHelper{}
}

// FindRepositoryDirectoryFromBase finds the actual repository directory within the base directory.
// Handles two layouts:
//  1. baseDir itself is a repo (baseDir/.git is a directory or a worktree gitfile).
//  2. baseDir contains a single cloned repo as a subdirectory (baseDir/<name>/.git exists).
//
// Worktrees created via "git worktree add" use a regular *file* at .git (not a directory)
// containing "gitdir: <basePath>", so existence — not directory-ness — is the right predicate.
func (rh *RepositoryHelper) FindRepositoryDirectoryFromBase(baseDir string) string {
	// First, check if base directory itself is a git repository (regular clone or worktree).
	if hasGitEntry(baseDir) {
		return baseDir
	}

	// Look for subdirectories that contain a .git entry (file or directory).
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return baseDir // fallback to base directory
	}

	for _, entry := range entries {
		if entry.IsDir() {
			dirPath := filepath.Join(baseDir, entry.Name())
			if hasGitEntry(dirPath) {
				return dirPath
			}
		}
	}

	// If no git repository found, return the base directory
	return baseDir
}

// hasGitEntry reports whether dir/.git exists, accepting both a regular .git directory
// (standard clone) and a .git gitfile (created by "git worktree add").
func hasGitEntry(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// GetWorkingDirectoryWithInjection handles working directory injection and repository discovery
// This is the standard pattern for tools that work with repository files
func (rh *RepositoryHelper) GetWorkingDirectoryWithInjection(input map[string]any, defaultWorkspaceDir string) string {
	// Use injected working directory if available, otherwise use default workspace
	workingDir := defaultWorkspaceDir
	if injectedWorkingDir, ok := input["working_directory"].(string); ok && injectedWorkingDir != "" {
		workingDir = injectedWorkingDir
	}

	// Find the actual repository directory within working directory
	return rh.FindRepositoryDirectoryFromBase(workingDir)
}
