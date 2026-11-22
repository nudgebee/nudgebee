package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// absenceRepo writes files into a temporary directory and returns a search
// tool rooted there. The tests need the ripgrep binary, as the tool does.
func absenceRepo(t *testing.T, files map[string]string) (*RipgrepTool, string) {
	t.Helper()
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep is not installed")
	}
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	return NewRipgrepTool(dir), dir
}

func search(t *testing.T, tool *RipgrepTool, dir string, input map[string]any) (string, map[string]any) {
	t.Helper()
	input["working_directory"] = dir
	resp := tool.Execute(context.Background(), input)
	require.Equal(t, "success", resp.Status, resp.Error)
	data, _ := resp.Data.(map[string]any)
	return resp.Observation, data
}

func TestNoMatch_NameThatIsNowhere(t *testing.T) {
	tool, dir := absenceRepo(t, map[string]string{
		"api/budget.go":      "package api\n\nfunc maxAttempts() int { return 3 }\n",
		"api/budget_test.go": "package api\n\nfunc TestResolve(t *testing.T) {}\n",
	})

	obs, data := search(t, tool, dir, map[string]any{"pattern": "RetryBudgetPolicy"})

	assert.Contains(t, obs, "No match anywhere in this repository for `RetryBudgetPolicy`")
	assert.Contains(t, obs, "it does not exist here")
	assert.Contains(t, obs, "Not searched: files the repository ignores")
	assert.Equal(t, true, data["definitive_no_match"])
}

// The search tool leaves out test files unless asked. A name that lives only
// in a test file is not absent, and must not be reported as absent.
func TestNoMatch_NameOnlyInATestFile(t *testing.T) {
	tool, dir := absenceRepo(t, map[string]string{
		"common/broker.go":      "package common\n",
		"common/broker_test.go": "package common\n\nconst flag = \"REQUIRE_MESSAGE_BROKER\"\n",
	})

	obs, data := search(t, tool, dir, map[string]any{"pattern": "REQUIRE_MESSAGE_BROKER"})

	assert.Contains(t, obs, "but it is in the repository")
	assert.Contains(t, obs, "File: common/broker_test.go — 1 match(es) @ lines 3")
	assert.NotContains(t, obs, "does not exist")
	assert.Equal(t, true, data["found_elsewhere"])
}

func TestNoMatch_NameHiddenByTheCallersFilters(t *testing.T) {
	files := map[string]string{
		"billing/charge.go": "package billing\n\nfunc ChargeCard() {}\n",
		"api/handler.go":    "package api\n",
		"docs/notes.md":     "see charge_card for details\n",
	}
	tests := []struct {
		name  string
		input map[string]any
		want  string
	}{
		{"a path filter", map[string]any{"pattern": "ChargeCard", "path": "api"}, "File: billing/charge.go"},
		{"a type filter", map[string]any{"pattern": "ChargeCard", "type": "py"}, "File: billing/charge.go"},
		{"another letter case", map[string]any{"pattern": "chargecard"}, "File: billing/charge.go"},
		{"its words joined another way", map[string]any{"pattern": "charge-card"}, "File: docs/notes.md"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tool, dir := absenceRepo(t, files)
			obs, data := search(t, tool, dir, tc.input)
			assert.Contains(t, obs, "but it is in the repository")
			assert.Contains(t, obs, tc.want)
			assert.Equal(t, true, data["found_elsewhere"])
		})
	}
}

// A name seen in a log is often put together at run time. The whole name is
// then nowhere in the source while the part that matters is.
func TestNoMatch_NameBuiltAtRunTime(t *testing.T) {
	tool, dir := absenceRepo(t, map[string]string{
		"system/manager.go": "package system\n\nfunc id(name string) string { return \"cron-\" + name }\n",
		"api/cron.go":       "package api\n\nconst job = \"nightly_inventory_sync\"\n",
	})

	obs, data := search(t, tool, dir, map[string]any{"pattern": "cron-nightly_inventory_sync"})

	assert.Contains(t, obs, "but part of it, `nightly_inventory_sync`, is here")
	assert.Contains(t, obs, "File: api/cron.go — 1 match(es) @ lines 3")
	assert.Contains(t, obs, "put together at run time")
	assert.NotContains(t, obs, "does not exist")
	assert.Equal(t, "nightly_inventory_sync", data["part_found"])
}

// "No match" for a regular expression says nothing about what exists, so no
// verdict is given for one.
func TestNoMatch_RegularExpressionGetsNoVerdict(t *testing.T) {
	tool, dir := absenceRepo(t, map[string]string{"a.go": "package a\n"})

	obs, data := search(t, tool, dir, map[string]any{"pattern": `func \(t \*Tool\) Call`})

	assert.Equal(t, noMatchObservation, obs)
	assert.Nil(t, data["definitive_no_match"])
}

// Absence is stated only after a search that ran to the end.
func TestNoMatch_NoVerdictWhenTheCheckCouldNotFinish(t *testing.T) {
	tool, dir := absenceRepo(t, map[string]string{"a.go": "package a\n"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resp := tool.noMatchVerdict(ctx, "RetryBudgetPolicy", dir)

	assert.Equal(t, noMatchObservation, resp.Observation)
	data, _ := resp.Data.(map[string]any)
	assert.Nil(t, data["definitive_no_match"])
}

func TestIsPlainName(t *testing.T) {
	for _, name := range []string{"RetryBudgetPolicy", "nightly_inventory_sync", "deploy-webhook", "main.go", "ERR7000215"} {
		assert.True(t, isPlainName(name), name)
	}
	for _, pattern := range []string{"abc", `func \(t`, "Foo|Bar", "a.*b", "two words", "path/to/file.go", `"quoted"`, "x+y", "____", "a__b", "-.-.", ""} {
		assert.False(t, isPlainName(pattern), pattern)
	}
}

func TestLooseNamePattern(t *testing.T) {
	tests := []struct{ name, want string }{
		{"RetryBudgetPolicy", `Retry[_\-.]?Budget[_\-.]?Policy`},
		{"retry_budget_policy", `retry[_\-.]?budget[_\-.]?policy`},
		{"deploy-webhook", `deploy[_\-.]?webhook`},
		{"main.go", `main[_\-.]?go`},
		{"ERR7000215", "ERR7000215"},
		// A run of capitals ends where a capitalised word begins.
		{"HTTPServer", `HTTP[_\-.]?Server`},
		{"parseURLPath", `parse[_\-.]?URL[_\-.]?Path`},
		{"IDToken", `ID[_\-.]?Token`},
		{"REQUIRE_MESSAGE_BROKER", `REQUIRE[_\-.]?MESSAGE[_\-.]?BROKER`},
		{"____", ""},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, looseNamePattern(tc.name), tc.name)
	}
}

func TestNameParts(t *testing.T) {
	tests := []struct {
		name string
		want []string
	}{
		{"cron-nightly_inventory_sync", []string{"nightly_inventory_sync"}},
		{"app_http_request_seconds", []string{"http_request_seconds", "app_http_request"}},
		// One short common word is not a part worth reporting.
		{"test-pool-saturated", nil},
		{"deploy-webhook", nil},
		{"RunCliCommand", nil},
		{"main.go", nil},
	}
	for _, tc := range tests {
		got := nameParts(tc.name)
		assert.Equal(t, tc.want, got, tc.name)
		assert.LessOrEqual(t, len(got), absencePartsTried)
		assert.NotContains(t, strings.Join(got, "\n"), "\n"+tc.name+"\n")
	}
}

// A name can exist only as the name of a file. The search reads contents, so
// without a look at file names it would call such a name absent.
func TestNoMatch_NameThatIsOnlyAFileName(t *testing.T) {
	tool, dir := absenceRepo(t, map[string]string{
		"playbooks/action_export_report.go": "package playbooks\n\nfunc Run() {}\n",
		"playbooks/other.go":                "package playbooks\n",
	})

	obs, data := search(t, tool, dir, map[string]any{"pattern": "action_export_report"})

	assert.Contains(t, obs, "but it is in the repository")
	assert.Contains(t, obs, "File named like it: playbooks/action_export_report.go")
	assert.NotContains(t, obs, "does not exist")
	assert.Equal(t, true, data["found_elsewhere"])
}

// Workflow files and other dot-directories are part of the repository.
func TestNoMatch_NameInAHiddenDirectory(t *testing.T) {
	tool, dir := absenceRepo(t, map[string]string{
		".github/workflows/deploy.yaml": "jobs:\n  deploy-webhook:\n    runs-on: ubuntu\n",
		"main.go":                       "package main\n",
	})

	obs, _ := search(t, tool, dir, map[string]any{"pattern": "deploy-webhook", "path": "."})

	assert.Contains(t, obs, "File: .github/workflows/deploy.yaml — 1 match(es) @ lines 2")
	assert.NotContains(t, obs, "does not exist")
}

// parseURLPath and parse_url_path are the same name.
func TestNoMatch_AcronymInACamelCaseName(t *testing.T) {
	tool, dir := absenceRepo(t, map[string]string{"web/url.py": "def parse_url_path(raw):\n    return raw\n"})

	obs, _ := search(t, tool, dir, map[string]any{"pattern": "parseURLPath"})

	assert.Contains(t, obs, "File: web/url.py — 1 match(es) @ lines 1")
	assert.NotContains(t, obs, "does not exist")
}

// A pattern with no letters in it is not a name; it must not turn into an
// expression that matches every line.
func TestNoMatch_SeparatorsOnlyGetNoVerdict(t *testing.T) {
	tool, dir := absenceRepo(t, map[string]string{"a.go": "package a\n"})

	resp := tool.noMatchVerdict(context.Background(), "____", dir)

	assert.Equal(t, noMatchObservation, resp.Observation)
}

// A name found in hundreds of files is reported with a short list and a note
// that there are more.
func TestNoMatch_CommonNameIsCutShort(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 300; i++ {
		files[fmt.Sprintf("pkg/f%03d.go", i)] = "package pkg\n\nvar shared_counter_value = 1\nvar shared_counter_value2 = 2\n"
	}
	tool, dir := absenceRepo(t, files)

	obs, _ := search(t, tool, dir, map[string]any{"pattern": "SharedCounterValue", "path": "pkg", "type": "py"})

	assert.Contains(t, obs, "but it is in the repository")
	assert.Contains(t, obs, "… and more file(s).")
	assert.LessOrEqual(t, strings.Count(obs, "File: "), absenceFilesListed)
}
