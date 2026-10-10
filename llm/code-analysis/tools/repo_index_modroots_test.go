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

// A Node module is only given `npm run build` when its manifest defines that
// script. Without one the command fails for a reason unrelated to the code, so
// the module gets no build command and the prompt lists it without one.
func TestFindModuleRoots_NodeBuildOnlyWhenTheManifestDefinesIt(t *testing.T) {
	root := t.TempDir()
	mk := func(rel, manifest string) {
		dir := filepath.Join(root, rel)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("web", `{"name": "web", "scripts": {"build": "tsc", "test": "jest"}}`)
	mk("e2e", `{"name": "e2e", "scripts": {"test": "playwright test"}}`)
	mk("lib", `{"name": "lib"}`)

	build := map[string]string{}
	for _, m := range findModuleRoots(root) {
		if m.Kind != "node" {
			t.Fatalf("unexpected module %+v", m)
		}
		build[m.Path] = m.Build
	}
	if len(build) != 3 {
		t.Fatalf("expected three node modules, got %+v", build)
	}
	if build["web"] != "npm run build" {
		t.Errorf("web defines a build script, got build command %q", build["web"])
	}
	for _, dir := range []string{"e2e", "lib"} {
		if build[dir] != "" {
			t.Errorf("%s defines no build script, got build command %q", dir, build[dir])
		}
	}

	idx := &RepoIndex{ModuleRoots: findModuleRoots(root)}
	for name, text := range map[string]string{"facts": idx.FormatFacts(), "context": idx.FormatAsContext()} {
		if !strings.Contains(text, "web (node) — verify: cd web && npm run build") {
			t.Errorf("%s: web must keep its verify command:\n%s", name, text)
		}
		if !strings.Contains(text, "e2e (node) — no build command defined") {
			t.Errorf("%s: e2e must be listed without a verify command:\n%s", name, text)
		}
		if strings.Contains(text, "cd e2e &&") || strings.Contains(text, "cd lib &&") {
			t.Errorf("%s: a module without a build script was given a command to run:\n%s", name, text)
		}
	}
}

// Only a script named exactly `build`, inside the manifest's own `scripts` block,
// counts. Anything else leaves the module without a build command.
func TestPackageJSONHasBuildScript(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		want     bool
	}{
		{"build script", `{"scripts": {"build": "tsc"}}`, true},
		{"build among others", `{"scripts": {"test": "jest", "build": "vite build"}}`, true},
		{"no scripts block", `{"name": "lib"}`, false},
		{"empty scripts block", `{"scripts": {}}`, false},
		{"only prebuild", `{"scripts": {"prebuild": "rm -rf dist"}}`, false},
		{"only build:prod", `{"scripts": {"build:prod": "vite build"}}`, false},
		{"build is a dependency, not a script", `{"scripts": {"test": "jest"}, "dependencies": {"build": "1.0.0"}}`, false},
		{"brace inside a script value", `{"scripts": {"test": "echo {", "lint": "eslint ."}, "dependencies": {"build": "1.0.0"}}`, false},
		{"the word scripts in another field", `{"description": "has \"scripts\": {\"build\": 1}", "files": ["scripts"]}`, false},
		{"not valid JSON", `{"scripts": {"build": "tsc"`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "package.json")
			if err := os.WriteFile(path, []byte(c.manifest), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := packageJSONHasBuildScript(path); got != c.want {
				t.Errorf("packageJSONHasBuildScript = %v, want %v for %s", got, c.want, c.manifest)
			}
		})
	}
	if packageJSONHasBuildScript(filepath.Join(t.TempDir(), "missing.json")) {
		t.Error("a manifest that cannot be read must not be given a build command")
	}
}
