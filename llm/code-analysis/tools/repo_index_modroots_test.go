package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// findModuleRoots must discover manifests in nested directories (the monorepo case),
// not just the repo root, and skip dependency dirs.
func TestFindModuleRoots_Monorepo(t *testing.T) {
	root := t.TempDir()
	mk := func(rel, file string) {
		dir := filepath.Join(root, rel)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, file), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mk("api-server/services", "go.mod") // nested Go module (the real monorepo shape)
	mk("app", "package.json")           // nested Node module
	mk("vendor/github.com/x", "go.mod") // dependency — must be skipped

	roots := findModuleRoots(root)

	got := map[string]string{}
	for _, m := range roots {
		got[m.Path] = m.Kind
	}
	if got[filepath.Join("api-server", "services")] != "go" {
		t.Errorf("expected nested go module at api-server/services, got %+v", roots)
	}
	if got["app"] != "node" {
		t.Errorf("expected node module at app, got %+v", roots)
	}
	for _, m := range roots {
		if strings.HasPrefix(m.Path, "vendor") {
			t.Errorf("vendor dir must be skipped, got %+v", m)
		}
	}
}
