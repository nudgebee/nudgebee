package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// instructionsRequireWrite gates the orchestrator's "file must exist before
// running CodeFixer" precondition. It must say true if and only if at least
// one implementation_instruction uses action="write", because that's the
// only legitimate way to bypass the file-exists check without re-opening
// the hallucinated-path failure mode the gate was originally designed to
// catch.
func TestInstructionsRequireWrite_NoInstructions(t *testing.T) {
	cases := []map[string]any{
		nil,
		{},
		{"implementation_instructions": nil},
		{"implementation_instructions": []any{}},
		{"implementation_instructions": "not a list"}, // malformed
	}
	for i, c := range cases {
		if instructionsRequireWrite(c) {
			t.Errorf("case %d: expected false for %+v", i, c)
		}
	}
}

func TestInstructionsRequireWrite_OnlyReplace(t *testing.T) {
	facts := map[string]any{
		"implementation_instructions": []any{
			map[string]any{"action": "replace", "file_path": "a.go"},
			map[string]any{"action": "verify", "file_path": "b.go"},
		},
	}
	if instructionsRequireWrite(facts) {
		t.Error("replace+verify should not bypass the file-exists check")
	}
}

func TestInstructionsRequireWrite_AnyWriteWins(t *testing.T) {
	facts := map[string]any{
		"implementation_instructions": []any{
			map[string]any{"action": "replace", "file_path": "existing.go"},
			map[string]any{"action": "write", "file_path": "new.yaml"},
		},
	}
	if !instructionsRequireWrite(facts) {
		t.Error("a single action=write should trigger the bypass")
	}
}

func TestInstructionsRequireWrite_TolerantToBadEntries(t *testing.T) {
	facts := map[string]any{
		"implementation_instructions": []any{
			"not a map",
			map[string]any{"step": 1},    // missing action
			map[string]any{"action": 42}, // non-string action
			map[string]any{"action": "write"},
		},
	}
	if !instructionsRequireWrite(facts) {
		t.Error("malformed siblings should not mask a real write action")
	}
}

// repoRelativeFilePath strips a single leading "<repoName>/" segment so the
// orchestrator's file-exists gate joins ripgrep-relative paths against the repo
// root without doubling the segment. A doubled path made os.Stat report the file
// missing, silently skipping CodeFixer and forcing an expensive re-invocation.
func TestRepoRelativeFilePath(t *testing.T) {
	repo := filepath.Join("tmp", "code-analysis-abc", "nudgebee-enterprise")
	cases := []struct {
		name     string
		workDir  string
		filePath string
		want     string
	}{
		{
			name:     "strips matching repo-name prefix (the bug)",
			workDir:  repo,
			filePath: "nudgebee-enterprise/api-server/services/relay/service.go",
			want:     "api-server/services/relay/service.go",
		},
		{
			name:     "already repo-relative is unchanged",
			workDir:  repo,
			filePath: "api-server/services/relay/service.go",
			want:     "api-server/services/relay/service.go",
		},
		{
			name:     "only the leading occurrence is stripped",
			workDir:  repo,
			filePath: "nudgebee-enterprise/x/nudgebee-enterprise/y.go",
			want:     "x/nudgebee-enterprise/y.go",
		},
		{
			name:     "non-matching prefix is left intact",
			workDir:  repo,
			filePath: "some-other-repo/api-server/main.go",
			want:     "some-other-repo/api-server/main.go",
		},
		{
			name:     "a directory merely sharing the repo-name prefix is not stripped",
			workDir:  repo,
			filePath: "nudgebee-enterprise-extras/main.go",
			want:     "nudgebee-enterprise-extras/main.go",
		},
		{
			name:     "empty workDir returns input unchanged",
			workDir:  "",
			filePath: "nudgebee-enterprise/api-server/main.go",
			want:     "nudgebee-enterprise/api-server/main.go",
		},
		{
			name:     "empty filePath returns empty",
			workDir:  repo,
			filePath: "",
			want:     "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := repoRelativeFilePath(c.workDir, c.filePath); got != c.want {
				t.Errorf("repoRelativeFilePath(%q, %q) = %q, want %q", c.workDir, c.filePath, got, c.want)
			}
		})
	}
}

// A diff that creates two files and edits a third, as `git diff` prints it.
const diffAddingTwoFiles = `diff --git a/e2e/tests/search.spec.ts b/e2e/tests/search.spec.ts
new file mode 100644
index 0000000..1c2d3e4
--- /dev/null
+++ b/e2e/tests/search.spec.ts
@@ -0,0 +1,3 @@
+import { test } from "@playwright/test";
+
+test("search", async () => {});
diff --git a/e2e/tests/locators.ts b/e2e/tests/locators.ts
new file mode 100644
index 0000000..5f6a7b8
--- /dev/null
+++ b/e2e/tests/locators.ts
@@ -0,0 +1 @@
+export const searchBox = "search";
diff --git a/e2e/README.md b/e2e/README.md
index 1111111..2222222 100644
--- a/e2e/README.md
+++ b/e2e/README.md
@@ -1 +1,2 @@
 # e2e
+See tests/.
`

// diffAddsFile is the second way past the "file must exist" precondition: a
// request that carries the diff to apply names files that diff creates. It must
// say true only for a file the diff adds — a file the diff merely edits, or
// does not mention, is still expected to be on disk.
func TestDiffAddsFile(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "shop")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		diff     string
		filePath string
		want     bool
	}{
		{"first added file", diffAddingTwoFiles, "e2e/tests/search.spec.ts", true},
		{"second added file", diffAddingTwoFiles, "e2e/tests/locators.ts", true},
		{"repository name in front of the path", diffAddingTwoFiles, "shop/e2e/tests/locators.ts", true},
		{"path written with a leading ./", diffAddingTwoFiles, "./e2e/tests/locators.ts", true},
		{"windows line endings", strings.ReplaceAll(diffAddingTwoFiles, "\n", "\r\n"), "e2e/tests/locators.ts", true},
		{"file the diff edits but does not add", diffAddingTwoFiles, "e2e/README.md", false},
		{"file the diff does not mention", diffAddingTwoFiles, "e2e/tests/other.ts", false},
		{"path git wrote in quotes", "--- /dev/null\n+++ \"b/e2e/tests/caf\\303\\251.ts\"\n@@ -0,0 +1 @@\n+x\n", "e2e/tests/café.ts", true},
		{"a +++ line that does not directly follow --- /dev/null", "--- /dev/null\n@@ -0,0 +1 @@\n+++ b/e2e/tests/locators.ts\n", "e2e/tests/locators.ts", false},
		{"no diff supplied", "", "e2e/tests/locators.ts", false},
		{"no file named", diffAddingTwoFiles, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := diffAddsFile(c.diff, workDir, c.filePath); got != c.want {
				t.Errorf("diffAddsFile(%q) = %v, want %v", c.filePath, got, c.want)
			}
		})
	}
}

// namedFileMissing is the whole decision to skip the fixer: the named file is
// not in the checkout and nothing in the request creates it. A file the fix
// itself creates — by a write instruction or by the supplied diff — is absent
// for a legitimate reason and must reach the fixer.
func TestNamedFileMissing(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "shop")
	if err := os.MkdirAll(filepath.Join(workDir, "e2e"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "e2e", "README.md"), []byte("# e2e\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTo := func(path string) []any {
		return []any{map[string]any{"action": "write", "file_path": path}}
	}
	cases := []struct {
		name     string
		facts    map[string]any
		baseDiff string
		want     bool
	}{
		{"file is in the checkout", map[string]any{"file_path": "e2e/README.md"}, "", false},
		{"file is in the checkout, repository name in front", map[string]any{"file_path": "shop/e2e/README.md"}, "", false},
		{"file is absent and nothing creates it", map[string]any{"file_path": "e2e/tests/locators.ts"}, "", true},
		{"file is absent, a write instruction creates it", map[string]any{"file_path": "e2e/tests/locators.ts", "implementation_instructions": writeTo("e2e/tests/locators.ts")}, "", false},
		{"file is absent, the supplied diff adds it", map[string]any{"file_path": "e2e/tests/locators.ts"}, diffAddingTwoFiles, false},
		{"file is absent, the supplied diff adds it, repository name in front", map[string]any{"file_path": "shop/e2e/tests/locators.ts"}, diffAddingTwoFiles, false},
		{"file is absent, the supplied diff adds other files only", map[string]any{"file_path": "e2e/tests/other.ts"}, diffAddingTwoFiles, true},
		{"no file named", map[string]any{}, diffAddingTwoFiles, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := namedFileMissing(c.facts, c.baseDiff, workDir); got != c.want {
				t.Errorf("namedFileMissing = %v, want %v", got, c.want)
			}
		})
	}
	if namedFileMissing(map[string]any{"file_path": "e2e/tests/locators.ts"}, "", "") {
		t.Error("with no checkout directory known there is nothing to check against")
	}
}
