package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SkipDirs lists directories to skip during repo indexing.
var SkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
	".tox":         true,
	"dist":         true,
	"build":        true,
	".next":        true,
	".cache":       true,
	".mypy_cache":  true,
	"coverage":     true,
	".terraform":   true,
}

// RepoIndex holds structural information about a repository.
type RepoIndex struct {
	FileTree        string            `json:"file_tree"`
	Languages       map[string]int    `json:"languages"`
	PrimaryLanguage string            `json:"primary_language"`
	BuildSystem     string            `json:"build_system"`
	BuildCommands   map[string]string `json:"build_commands"`
	EntryPoints     []string          `json:"entry_points"`
	ConfigFiles     []string          `json:"config_files"`
	ModuleRoots     []ModuleRoot      `json:"module_roots"`
	TotalFiles      int               `json:"total_files"`
}

// ModuleRoot is a discovered build unit (a directory containing a package manifest).
// In a monorepo the manifest is usually NOT at the repo root, so verifying a fix
// requires building/testing from the right module directory. Discovered by walking
// the tree — nothing about a specific repo's layout is hardcoded.
type ModuleRoot struct {
	Path  string `json:"path"`  // dir (relative to repo root) containing the manifest; "." for root
	Kind  string `json:"kind"`  // go | node | python | rust | java
	Build string `json:"build"` // suggested verification command, run from Path
}

type dirEntry struct {
	name      string
	relPath   string
	fileCount int
	isDir     bool
}

// IndexRepository scans a repo directory and returns a structural summary.
func IndexRepository(repoDir string) (*RepoIndex, error) {
	if _, err := os.Stat(repoDir); err != nil {
		return nil, fmt.Errorf("repo dir not found: %w", err)
	}

	idx := &RepoIndex{
		Languages:     make(map[string]int),
		BuildCommands: make(map[string]string),
	}

	var topEntries []dirEntry

	// Walk top 2 directory levels
	err := filepath.Walk(repoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		rel, _ := filepath.Rel(repoDir, path)
		if rel == "." {
			return nil
		}

		depth := strings.Count(rel, string(filepath.Separator))

		// Skip ignored directories
		if info.IsDir() && SkipDirs[info.Name()] {
			return filepath.SkipDir
		}

		// Only index top 2 levels for tree
		if depth > 2 {
			if info.IsDir() {
				return filepath.SkipDir
			}
			if !info.IsDir() {
				idx.TotalFiles++
				ext := strings.ToLower(filepath.Ext(info.Name()))
				if ext != "" {
					idx.Languages[ext]++
				}
			}
			return nil
		}

		if !info.IsDir() {
			idx.TotalFiles++
			ext := strings.ToLower(filepath.Ext(info.Name()))
			if ext != "" {
				idx.Languages[ext]++
			}
		}

		// Build tree entries for top level
		if depth == 1 {
			entry := dirEntry{
				name:    info.Name(),
				relPath: rel,
				isDir:   info.IsDir(),
			}
			if info.IsDir() {
				entry.fileCount = countFiles(path)
			}
			topEntries = append(topEntries, entry)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk failed: %w", err)
	}

	idx.PrimaryLanguage = findPrimaryLanguage(idx.Languages)
	idx.BuildSystem, idx.BuildCommands = detectBuildSystem(repoDir)
	idx.EntryPoints = findEntryPoints(repoDir)
	idx.ConfigFiles = findConfigFiles(repoDir)
	idx.ModuleRoots = findModuleRoots(repoDir)
	idx.FileTree = buildTree(topEntries)

	return idx, nil
}

// moduleManifests maps a package-manifest filename to (kind, suggested build cmd).
// Discovery is data-driven from these manifests — no per-repo paths are hardcoded.
var moduleManifests = []struct {
	file  string
	kind  string
	build string
}{
	{"go.mod", "go", "go build ./..."},
	{"package.json", "node", "npm run build"},
	{"pyproject.toml", "python", "python -m build"},
	{"setup.py", "python", "python -m build"},
	{"Cargo.toml", "rust", "cargo build"},
	{"pom.xml", "java", "mvn -q compile"},
}

// findModuleRoots walks the repo (bounded depth, skipping vendor/deps dirs) and
// returns every directory that contains a package manifest, with the correct
// working directory + build command. In a monorepo the manifest is rarely at the
// repo root, so this is what lets the agent verify a fix from the right directory
// instead of guessing (e.g. `cd api-server && go build ./...` against a module
// rooted at api-server/services).
func findModuleRoots(repoDir string) []ModuleRoot {
	const maxDepth = 4
	const maxRoots = 25
	var roots []ModuleRoot
	skip := map[string]bool{"vendor": true, "node_modules": true, ".git": true, "dist": true, "build": true, "target": true, ".venv": true}
	seenDirs := make(map[string]bool)

	// WalkDir avoids an lstat per entry, and matching manifests by name during the
	// walk avoids up to len(moduleManifests) os.Stat calls per directory.
	_ = filepath.WalkDir(repoDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(repoDir, path)
		if d.IsDir() {
			if rel != "." {
				if skip[d.Name()] {
					return filepath.SkipDir
				}
				if strings.Count(rel, string(filepath.Separator))+1 > maxDepth {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if len(roots) >= maxRoots {
			return filepath.SkipDir
		}
		for _, m := range moduleManifests {
			if d.Name() == m.file {
				dir := filepath.Dir(rel)
				if !seenDirs[dir] {
					seenDirs[dir] = true
					roots = append(roots, ModuleRoot{Path: dir, Kind: m.kind, Build: m.build})
				}
				break // one kind per dir is enough
			}
		}
		return nil
	})
	return roots
}

// FormatAsContext returns compact text for injection into LLM context.
func (idx *RepoIndex) FormatAsContext() string {
	var b strings.Builder
	b.WriteString("## REPOSITORY STRUCTURE (snapshot from clone — use tools for current state)\n")

	if idx.PrimaryLanguage != "" {
		langName := extToLanguage(idx.PrimaryLanguage)
		fmt.Fprintf(&b, "Primary: %s (%d files)", langName, idx.Languages[idx.PrimaryLanguage])
	}

	if idx.BuildSystem != "" {
		if idx.PrimaryLanguage != "" {
			b.WriteString(" | ")
		}
		fmt.Fprintf(&b, "Build: %s", idx.BuildSystem)
		if len(idx.BuildCommands) > 0 {
			targets := make([]string, 0, len(idx.BuildCommands))
			for k := range idx.BuildCommands {
				targets = append(targets, k)
			}
			sort.Strings(targets)
			if len(targets) > 8 {
				targets = targets[:8]
			}
			fmt.Fprintf(&b, " (%s)", strings.Join(targets, ", "))
		}
	}
	b.WriteString("\n")

	if len(idx.ModuleRoots) > 0 {
		b.WriteString("Modules (build/verify a fix from the module's directory, not the repo root):\n")
		for _, m := range idx.ModuleRoots {
			loc := m.Path
			if loc == "." {
				loc = "<repo root>"
			}
			fmt.Fprintf(&b, "  - %s (%s) — verify: cd %s && %s\n", loc, m.Kind, m.Path, m.Build)
		}
	}

	if len(idx.EntryPoints) > 0 {
		fmt.Fprintf(&b, "Entry: %s\n", strings.Join(idx.EntryPoints, ", "))
	}

	if len(idx.ConfigFiles) > 0 {
		fmt.Fprintf(&b, "Config: %s\n", strings.Join(idx.ConfigFiles, ", "))
	}

	fmt.Fprintf(&b, "Total files: %d\n", idx.TotalFiles)

	if idx.FileTree != "" {
		b.WriteString("\nTree:\n")
		b.WriteString(idx.FileTree)
	}

	return b.String()
}

func countFiles(dir string) int {
	count := 0
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && SkipDirs[info.Name()] && path != dir {
			return filepath.SkipDir
		}
		if !info.IsDir() {
			count++
		}
		return nil
	})
	return count
}

func findPrimaryLanguage(languages map[string]int) string {
	if len(languages) == 0 {
		return ""
	}
	maxCount := 0
	maxExt := ""
	for ext, count := range languages {
		if count > maxCount {
			maxCount = count
			maxExt = ext
		}
	}
	return maxExt
}

func extToLanguage(ext string) string {
	m := map[string]string{
		".go": "Go", ".py": "Python", ".js": "JavaScript", ".ts": "TypeScript",
		".tsx": "TypeScript", ".jsx": "JavaScript", ".java": "Java", ".rs": "Rust",
		".rb": "Ruby", ".php": "PHP", ".cs": "C#", ".cpp": "C++", ".c": "C",
		".sh": "Shell", ".yaml": "YAML", ".yml": "YAML", ".json": "JSON",
		".md": "Markdown", ".sql": "SQL",
	}
	if name, ok := m[ext]; ok {
		return name
	}
	return strings.TrimPrefix(ext, ".")
}

func detectBuildSystem(repoDir string) (string, map[string]string) {
	commands := make(map[string]string)

	if data, err := os.ReadFile(filepath.Join(repoDir, "Makefile")); err == nil {
		for _, t := range parseMakefileTargets(string(data)) {
			commands[t] = "make " + t
		}
		return "Makefile", commands
	}

	if data, err := os.ReadFile(filepath.Join(repoDir, "package.json")); err == nil {
		for _, s := range parsePackageJSONScripts(string(data)) {
			commands[s] = "npm run " + s
		}
		return "npm", commands
	}

	if _, err := os.Stat(filepath.Join(repoDir, "go.mod")); err == nil {
		commands["build"] = "go build ./..."
		commands["test"] = "go test ./..."
		return "go.mod", commands
	}

	if _, err := os.Stat(filepath.Join(repoDir, "pyproject.toml")); err == nil {
		commands["install"] = "poetry install"
		commands["test"] = "poetry run pytest"
		return "pyproject.toml", commands
	}

	if _, err := os.Stat(filepath.Join(repoDir, "Cargo.toml")); err == nil {
		commands["build"] = "cargo build"
		commands["test"] = "cargo test"
		return "Cargo.toml", commands
	}

	if _, err := os.Stat(filepath.Join(repoDir, "pom.xml")); err == nil {
		commands["build"] = "mvn package"
		commands["test"] = "mvn test"
		return "Maven", commands
	}

	return "", commands
}

var makeTargetRegex = regexp.MustCompile(`(?m)^([a-zA-Z_][a-zA-Z0-9_-]*)\s*:`)

func parseMakefileTargets(content string) []string {
	matches := makeTargetRegex.FindAllStringSubmatch(content, -1)
	seen := make(map[string]bool)
	var targets []string
	for _, m := range matches {
		name := m[1]
		if !seen[name] {
			seen[name] = true
			targets = append(targets, name)
		}
	}
	return targets
}

func parsePackageJSONScripts(content string) []string {
	scriptsIdx := strings.Index(content, `"scripts"`)
	if scriptsIdx == -1 {
		return nil
	}
	rest := content[scriptsIdx:]
	braceIdx := strings.Index(rest, "{")
	if braceIdx == -1 {
		return nil
	}
	depth := 0
	var block string
	for i := braceIdx; i < len(rest); i++ {
		if rest[i] == '{' {
			depth++
		} else if rest[i] == '}' {
			depth--
			if depth == 0 {
				block = rest[braceIdx : i+1]
				break
			}
		}
	}
	if block == "" {
		return nil
	}
	keyRegex := regexp.MustCompile(`"([^"]+)"\s*:`)
	matches := keyRegex.FindAllStringSubmatch(block, -1)
	var scripts []string
	for _, m := range matches {
		scripts = append(scripts, m[1])
	}
	return scripts
}

func findEntryPoints(repoDir string) []string {
	entryPatterns := []string{"main.*", "app.*", "index.*", "server.*"}
	entryDirs := []string{"", "cmd", "src"}

	var entries []string
	seen := make(map[string]bool)

	for _, dir := range entryDirs {
		searchDir := repoDir
		if dir != "" {
			searchDir = filepath.Join(repoDir, dir)
		}
		if _, err := os.Stat(searchDir); err != nil {
			continue
		}
		dirEntries, _ := os.ReadDir(searchDir)
		for _, e := range dirEntries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			for _, pattern := range entryPatterns {
				matched, _ := filepath.Match(pattern, name)
				if matched {
					ext := filepath.Ext(name)
					if ext == ".md" || ext == ".txt" || ext == ".json" || ext == ".yaml" || ext == ".yml" || ext == ".toml" || ext == ".lock" {
						continue
					}
					relPath := name
					if dir != "" {
						relPath = filepath.Join(dir, name)
					}
					if !seen[relPath] {
						seen[relPath] = true
						entries = append(entries, relPath)
					}
				}
			}
		}
	}

	if len(entries) > 5 {
		entries = entries[:5]
	}
	return entries
}

func findConfigFiles(repoDir string) []string {
	configPatterns := []string{
		"Dockerfile", "docker-compose.yml", "docker-compose.yaml",
		".env.example", "Makefile", "go.mod", "pyproject.toml",
		"package.json", "Cargo.toml", "pom.xml",
	}
	var configs []string
	for _, pattern := range configPatterns {
		if _, err := os.Stat(filepath.Join(repoDir, pattern)); err == nil {
			configs = append(configs, pattern)
		}
	}
	ciDirs := []string{".github/workflows", ".gitlab-ci.yml", ".circleci"}
	for _, ci := range ciDirs {
		if _, err := os.Stat(filepath.Join(repoDir, ci)); err == nil {
			configs = append(configs, ci)
		}
	}
	if len(configs) > 8 {
		configs = configs[:8]
	}
	return configs
}

func buildTree(entries []dirEntry) string {
	if len(entries) == 0 {
		return ""
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].isDir != entries[j].isDir {
			return entries[i].isDir
		}
		return entries[i].name < entries[j].name
	})
	var b strings.Builder
	for i, e := range entries {
		prefix := "├── "
		if i == len(entries)-1 {
			prefix = "└── "
		}
		if e.isDir {
			fmt.Fprintf(&b, "%s%s/ (%d files)\n", prefix, e.name, e.fileCount)
		} else {
			fmt.Fprintf(&b, "%s%s\n", prefix, e.name)
		}
	}
	return b.String()
}
