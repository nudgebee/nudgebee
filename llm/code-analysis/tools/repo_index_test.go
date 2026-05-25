package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("failed to mkdir %s: %v", path, err)
	}
}

func TestIndexRepository_GoProject(t *testing.T) {
	dir := t.TempDir()

	mustWriteFile(t, filepath.Join(dir, "go.mod"), []byte("module example.com/test\n\ngo 1.21\n"))
	mustWriteFile(t, filepath.Join(dir, "Makefile"), []byte("build:\n\tgo build\n\ntest:\n\tgo test ./...\n\nlint:\n\tgolangci-lint run\n"))
	mustMkdirAll(t, filepath.Join(dir, "cmd"))
	mustWriteFile(t, filepath.Join(dir, "cmd", "main.go"), []byte("package main\n\nfunc main() {}\n"))
	mustMkdirAll(t, filepath.Join(dir, "pkg", "handler"))
	mustWriteFile(t, filepath.Join(dir, "pkg", "handler", "handler.go"), []byte("package handler\n"))
	mustWriteFile(t, filepath.Join(dir, "pkg", "handler", "handler_test.go"), []byte("package handler\n"))
	mustWriteFile(t, filepath.Join(dir, "Dockerfile"), []byte("FROM golang:1.21\n"))

	idx, err := IndexRepository(dir)
	if err != nil {
		t.Fatalf("IndexRepository failed: %v", err)
	}
	if idx.TotalFiles == 0 {
		t.Error("expected non-zero TotalFiles")
	}
	if idx.PrimaryLanguage != ".go" {
		t.Errorf("expected primary language .go, got %s", idx.PrimaryLanguage)
	}
	if idx.BuildSystem != "Makefile" {
		t.Errorf("expected build system Makefile, got %s", idx.BuildSystem)
	}
	if _, ok := idx.BuildCommands["build"]; !ok {
		t.Error("expected 'build' in build commands")
	}
	if _, ok := idx.BuildCommands["test"]; !ok {
		t.Error("expected 'test' in build commands")
	}

	found := false
	for _, e := range idx.EntryPoints {
		if strings.Contains(e, "main.go") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected main.go in entry points, got %v", idx.EntryPoints)
	}
}

func TestIndexRepository_NodeProject(t *testing.T) {
	dir := t.TempDir()

	mustWriteFile(t, filepath.Join(dir, "package.json"), []byte(`{
  "name": "test-app",
  "scripts": {
    "dev": "next dev",
    "build": "next build",
    "test": "jest",
    "lint": "eslint ."
  }
}`))
	mustWriteFile(t, filepath.Join(dir, "index.ts"), []byte("export default {};\n"))

	idx, err := IndexRepository(dir)
	if err != nil {
		t.Fatalf("IndexRepository failed: %v", err)
	}
	if idx.BuildSystem != "npm" {
		t.Errorf("expected npm build system, got %s", idx.BuildSystem)
	}
}

func TestIndexRepository_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	idx, err := IndexRepository(dir)
	if err != nil {
		t.Fatalf("IndexRepository failed: %v", err)
	}
	if idx.TotalFiles != 0 {
		t.Errorf("expected 0 files, got %d", idx.TotalFiles)
	}
}

func TestIndexRepository_SkipsDotDirs(t *testing.T) {
	dir := t.TempDir()

	mustMkdirAll(t, filepath.Join(dir, ".git", "objects"))
	mustWriteFile(t, filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"))
	mustMkdirAll(t, filepath.Join(dir, "node_modules", "express"))
	mustWriteFile(t, filepath.Join(dir, "node_modules", "express", "index.js"), []byte(""))
	mustWriteFile(t, filepath.Join(dir, "main.go"), []byte("package main\n"))

	idx, err := IndexRepository(dir)
	if err != nil {
		t.Fatalf("IndexRepository failed: %v", err)
	}
	if idx.TotalFiles != 1 {
		t.Errorf("expected 1 file (skipping .git and node_modules), got %d", idx.TotalFiles)
	}
}

func TestParseMakefileTargets(t *testing.T) {
	content := "build:\n\tgo build\n\ntest:\n\tgo test\n\nlint:\n\tgolangci-lint run\n"
	targets := parseMakefileTargets(content)
	expected := map[string]bool{"build": true, "test": true, "lint": true}
	for _, tgt := range targets {
		if !expected[tgt] {
			t.Errorf("unexpected target: %s", tgt)
		}
	}
}

func TestParsePackageJSONScripts(t *testing.T) {
	content := `{"name":"app","scripts":{"dev":"next dev","build":"next build","lint":"eslint ."}}`
	scripts := parsePackageJSONScripts(content)
	if len(scripts) != 3 {
		t.Errorf("expected 3 scripts, got %d: %v", len(scripts), scripts)
	}
}

func TestFormatAsContext(t *testing.T) {
	idx := &RepoIndex{
		Languages:       map[string]int{".go": 45},
		PrimaryLanguage: ".go",
		BuildSystem:     "Makefile",
		BuildCommands:   map[string]string{"build": "make build", "test": "make test"},
		EntryPoints:     []string{"cmd/main.go"},
		ConfigFiles:     []string{"Dockerfile", "Makefile"},
		TotalFiles:      50,
		FileTree:        "├── cmd/ (1 files)\n└── Makefile\n",
	}
	output := idx.FormatAsContext()
	if !strings.Contains(output, "Go") {
		t.Error("expected 'Go' in output")
	}
	if !strings.Contains(output, "REPOSITORY STRUCTURE") {
		t.Error("expected header in output")
	}
}

func TestIndexRepository_NonExistent(t *testing.T) {
	_, err := IndexRepository("/nonexistent/path")
	if err == nil {
		t.Error("expected error for non-existent path")
	}
}
