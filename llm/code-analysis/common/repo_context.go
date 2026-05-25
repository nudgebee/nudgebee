package common

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RepoContextInfo provides shared context for all agents about repository structure
type RepoContextInfo struct {
	URL          string
	GitHubRepo   string
	Branch       string
	ClonedPath   string
	WorkspaceDir string
	AnalysisType string
	IsMonorepo   bool
	KnownModules []string
}

// GetMonorepoGuidance returns guidance text for agents working in monorepo context
func (r *RepoContextInfo) GetMonorepoGuidance() string {
	if !r.IsMonorepo {
		return ""
	}

	guidance := fmt.Sprintf(`
## REPOSITORY CONTEXT AWARENESS

**Current Repository:** %s
**Working Directory:** %s
**Repository Type:** Monorepo with multiple modules/services

**CRITICAL MONOREPO UNDERSTANDING:**
- When users mention service/module names (e.g., "llm-server", "api-server"), these are DIRECTORIES within this repository
- DO NOT treat them as separate GitHub repositories
- You are working within the cloned repository at: %s

**CORRECT COMMAND PATTERNS:**

For PR Analysis of modules:
- CORRECT: gh pr list --json number,title,files | jq '.[] | select(.files[]?.path? // "" | test("llm-server/"))'
- CORRECT: git log --oneline --since="1 month ago" -- llm-server/
- WRONG: gh pr list --repo llm-server (This will fail!)

For File Operations:
- CORRECT: find . -path "./llm-server/*" -name "*.py"
- CORRECT: grep -r "function_name" llm-server/
- CORRECT: ls -la llm-server/

**MODULE/SERVICE DETECTION:**
Common patterns: If user asks about "X", check if directory "X/" exists first.
`, r.GitHubRepo, r.WorkspaceDir, r.ClonedPath)

	return guidance
}

// GetScopeGuidance returns guidance about current working scope
func (r *RepoContextInfo) GetScopeGuidance() string {
	guidance := `
## WORKING SCOPE GUIDANCE

`
	// Check if repository is actually cloned and ready, not just workspace exists
	isRepositoryReady := r.isRepositoryActuallyCloned()

	if isRepositoryReady && r.WorkspaceDir != "" {
		guidance += fmt.Sprintf(`**Repository Cloned:** Yes
**Clone Location:** %s
**Working Directory:** %s

**File Access:** Available - you can read, search, and analyze files
**Git Operations:** Available - you can use git log, git blame, etc.
**GitHub Operations:** Available - you can use gh pr list, gh issues, etc.

`, r.ClonedPath, r.WorkspaceDir)
	} else if r.URL != "" && r.WorkspaceDir != "" {
		guidance += fmt.Sprintf(`**Repository URL Provided:** %s
**Workspace Directory:** %s
**Repository Status:** Not yet cloned

**CRITICAL:** Use repo_clone tool first to clone the repository before any file operations
**After Cloning:** File access, git operations, and GitHub operations will be available

`, r.URL, r.WorkspaceDir)
	} else {
		guidance += `**Repository Cloned:** No
**Working Mode:** Log-only analysis

**File Access:** Unavailable - file operations will return "unavailable"
**Git Operations:** Unavailable - no repository to analyze
**GitHub Operations:** Limited - can only work with provided information

`
	}

	return guidance
}

// FormatForAgent formats the context information for agent prompts
func (r *RepoContextInfo) FormatForAgent() string {
	var parts []string

	if r.URL != "" {
		parts = append(parts, fmt.Sprintf("Repository: %s", r.URL))
	}
	if r.GitHubRepo != "" {
		parts = append(parts, fmt.Sprintf("GitHub: %s", r.GitHubRepo))
	}
	if r.Branch != "" {
		parts = append(parts, fmt.Sprintf("Branch: %s", r.Branch))
	}
	if r.AnalysisType != "" {
		parts = append(parts, fmt.Sprintf("Analysis Type: %s", r.AnalysisType))
	}

	context := "## REPOSITORY CONTEXT\n"
	for _, part := range parts {
		context += fmt.Sprintf("- %s\n", part)
	}

	context += r.GetScopeGuidance()
	context += r.GetMonorepoGuidance()

	return context
}

// DetectMonorepoModules detects if this is a monorepo by scanning for multiple build system files
// in different subdirectories. Falls back to heuristics if ClonedPath is not available.
func (r *RepoContextInfo) DetectMonorepoModules() {
	// If repository is cloned, scan directories for build system files
	if r.ClonedPath != "" && r.isRepositoryActuallyCloned() {
		r.detectMonorepoByScanning()
		return
	}

	// Fallback: heuristic based on repo name (for pre-clone context)
	// This is a loose heuristic; the real detection happens after cloning.
	if strings.Contains(r.GitHubRepo, "nudgebee") {
		r.IsMonorepo = true
		r.KnownModules = []string{"llm-server", "api-server", "app"}
	}
}

// detectMonorepoByScanning walks the cloned repo looking for build system files in subdirectories.
// If 2+ different subdirectories contain their own build system files, it's a monorepo.
func (r *RepoContextInfo) detectMonorepoByScanning() {
	buildFiles := []string{"go.mod", "package.json", "pyproject.toml", "Cargo.toml", "pom.xml", "build.gradle", "Makefile"}
	moduleMap := make(map[string]bool)

	entries, err := os.ReadDir(r.ClonedPath)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Skip hidden dirs and common non-module dirs
		if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" ||
			name == "build" || name == "dist" || name == "__pycache__" || name == "deploy" {
			continue
		}

		// Check if this directory contains a build system file
		for _, bf := range buildFiles {
			buildPath := filepath.Join(r.ClonedPath, name, bf)
			if _, err := os.Stat(buildPath); err == nil {
				moduleMap[name] = true
				break
			}
		}
	}

	if len(moduleMap) >= 2 {
		r.IsMonorepo = true
		r.KnownModules = make([]string, 0, len(moduleMap))
		for mod := range moduleMap {
			r.KnownModules = append(r.KnownModules, mod)
		}
	}
}

// isRepositoryActuallyCloned checks if the repository is actually cloned and ready for file operations
func (r *RepoContextInfo) isRepositoryActuallyCloned() bool {
	if r.ClonedPath == "" || r.ClonedPath == "agent-managed" {
		return false
	}

	// Check if the directory exists and contains a .git directory
	gitDir := filepath.Join(r.ClonedPath, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return false
	}

	return true
}
