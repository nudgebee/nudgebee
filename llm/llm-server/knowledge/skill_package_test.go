package knowledge

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSkillFolder_FullSkill(t *testing.T) {
	files := map[string][]byte{
		"SKILL.md": []byte(`---
name: promql
description: Write and debug PromQL queries. Use when the user asks about Prometheus queries.
license: Apache-2.0
compatibility: Requires promtool on PATH
metadata:
  author: example-org
  version: "1.0"
allowed-tools: Bash(promtool:*) Read
---
# PromQL

Run ` + "`promtool check rules rules.yml`" + ` first.

` + "```bash\nkubectl get pods -n monitoring\npup logs search\n```" + `

See [the reference](references/functions.md).
`),
		"references/functions.md": []byte("rate() and irate()"),
		"scripts/check.py":        []byte("print('ok')"),
		"assets/logo.png":         {0x89, 'P', 'N', 'G', 0, 1},
	}
	pkg, err := ParseSkillFolder("skills/prometheus/promql", files, nil)
	require.NoError(t, err)

	assert.Equal(t, "promql", pkg.Name)
	assert.Equal(t, "Write and debug PromQL queries. Use when the user asks about Prometheus queries.", pkg.Description)
	assert.True(t, strings.HasPrefix(pkg.Body, "# PromQL"), "frontmatter stripped")
	assert.Equal(t, map[string]string{"references/functions.md": "rate() and irate()", "scripts/check.py": "print('ok')"}, pkg.Files)
	assert.Equal(t, "skills/prometheus/promql", pkg.Meta.SourcePath)
	assert.Equal(t, "Apache-2.0", pkg.Meta.License)
	assert.Equal(t, map[string]string{"author": "example-org", "version": "1.0"}, pkg.Meta.Metadata)
	assert.Equal(t, []SkillRequirement{
		{Kind: "note", Name: "Requires promtool on PATH"},
		{Kind: "cli", Name: "promtool"},
		{Kind: "cli", Name: "kubectl", Provided: true},
		{Kind: "cli", Name: "pup"},
		{Kind: "runtime", Name: "python", Provided: true},
	}, pkg.Meta.Requirements)
	assert.Contains(t, pkg.Meta.Warnings, "assets/logo.png skipped: not a text file")
	assert.Contains(t, pkg.Meta.Warnings, `needs cli "pup", which Nudgebee does not provide`)
	assert.NotContains(t, strings.Join(pkg.Meta.Warnings, "\n"), "kubectl")
	assert.Len(t, pkg.Meta.Files, 3)
}

func TestParseSkillFolder_Lenient(t *testing.T) {
	cases := []struct {
		name, dir, md string
		wantName      string
		wantWarning   string
		wantErr       string
	}{
		{"name differs from folder", "skills/a", "---\nname: b-skill\ndescription: d\n---\nbody", "b-skill", `name "b-skill" does not match its folder "a"`, ""},
		{"no name uses folder", "skills/k6", "---\ndescription: d\n---\nbody", "k6", `no name in SKILL.md; using the folder name "k6"`, ""},
		{"bad name still loads", "Pdf_Tool", "---\nname: Pdf_Tool\ndescription: d\n---\nbody", "Pdf_Tool", "is not 1-64 lowercase", ""},
		{"unquoted colon recovered", "x", "---\nname: x\ndescription: Use this when: the user asks about PDFs\n---\nbody", "x", "", ""},
		{"slash in name skipped", "x", "---\nname: a/b\ndescription: d\n---\nbody", "", "", "contains / or ,"},
		{"no description skipped", "x", "---\nname: x\n---\nbody", "", "", "has no description"},
		{"no header skipped", "x", "# just markdown", "", "", "does not start with"},
		{"unclosed header skipped", "x", "---\nname: x\n", "", "", "no closing"},
		{"broken yaml skipped", "x", "---\nname: x\n\tdescription: d\n---\n", "", "", "unreadable"},
		{"flow value recovered by quoting", "x", "---\nname: [x\ndescription: d\n---\n", "[x", "is not 1-64 lowercase", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pkg, err := ParseSkillFolder(tc.dir, map[string][]byte{"SKILL.md": []byte(tc.md)}, nil)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantName, pkg.Name)
			if tc.wantWarning != "" {
				assert.Contains(t, strings.Join(pkg.Meta.Warnings, "\n"), tc.wantWarning)
			}
		})
	}
	pkg, err := ParseSkillFolder("x", map[string][]byte{"SKILL.md": []byte("---\nname: x\ndescription: Use this when: the user asks\n---\nb")}, nil)
	require.NoError(t, err)
	assert.Equal(t, "Use this when: the user asks", pkg.Description)
}

func TestParseSkillFolder_LimitsAndUnsafePaths(t *testing.T) {
	big := []byte(strings.Repeat("a", SkillFileMaxBytes+1))
	files := map[string][]byte{
		"SKILL.md":          []byte("---\nname: x\ndescription: d\n---\nb"),
		"references/big.md": big,
		"../escape.md":      []byte("x"),
		"references/ok.md":  []byte("ok"),
		"references/./a.md": []byte("x"),
		"/abs.md":           []byte("x"),
		`references\win.md`: []byte("x"),
	}
	pkg, err := ParseSkillFolder("x", files, map[string]int64{"assets/video.mp4": 50 << 20})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"references/ok.md": "ok"}, pkg.Files)
	w := strings.Join(pkg.Meta.Warnings, "\n")
	assert.Contains(t, w, "references/big.md skipped: larger than 1 MB")
	assert.Contains(t, w, "../escape.md skipped: unsafe path")
	assert.Contains(t, w, "/abs.md skipped: unsafe path")
	assert.Contains(t, w, "assets/video.mp4 skipped: larger than 1 MB")
}

func TestDetectSkillRequirements_MCP(t *testing.T) {
	reqs := detectSkillRequirements("Use the Grafana MCP server, or mcp__datadog__search_logs.", "", "", []string{"scripts/run.sh", "scripts/x.rb"}, nil)
	assert.Equal(t, []SkillRequirement{
		{Kind: "runtime", Name: "shell", Provided: true},
		{Kind: "mcp", Name: "grafana"},
		{Kind: "mcp", Name: "datadog"},
	}, reqs)
}

func TestSkillLookupsFailClosedWithoutSecurityContext(t *testing.T) {
	f, err := GetSkillFile(nil, "acc-1", "promql", "references/fn.md")
	assert.Error(t, err)
	assert.Nil(t, f)
	skills, err := GetSkillsForCommand(nil, "acc-1", []string{"promql"})
	assert.Error(t, err)
	assert.Nil(t, skills)
}

func TestSkillIndexHeadingAndSnippet(t *testing.T) {
	assert.Equal(t, "# promql\n\nUse when writing PromQL.\n\n", skillIndexHeading(" promql ", " Use when writing PromQL. "))
	assert.Empty(t, skillIndexHeading("promql", "  "), "no description, body indexed alone")

	described := RAGSearchResult{Metadata: map[string]any{KnowledgeDescriptionKey: "Use when writing PromQL."}}
	assert.Equal(t, "Use when writing PromQL.", KnowledgeSnippetText(described, "# Setup\nInstall..."))
	assert.Equal(t, "# Setup", KnowledgeSnippetText(RAGSearchResult{Metadata: map[string]any{}}, "# Setup"))
}

// Packages a Python script imports are listed when they are known: the ones
// the workspace image lacks are flagged, the standard library and the skill's
// own modules are not listed.
func TestDetectSkillRequirements_PythonImports(t *testing.T) {
	files := map[string]string{
		"scripts/check.py": "import os, sys\nimport yaml\nfrom dateutil import parser\nfrom scripts.utils import x\n  import requests.adapters\nimport json, jinja2 as j\n# import pandas\n",
		"scripts/run.sh":   "import numpy\n",
	}
	reqs := detectSkillRequirements("", "", "", []string{"scripts/check.py", "scripts/run.sh"}, files)
	assert.Equal(t, []SkillRequirement{
		{Kind: "runtime", Name: "python", Provided: true},
		{Kind: "runtime", Name: "shell", Provided: true},
		{Kind: "python", Name: "pyyaml"},
		{Kind: "python", Name: "python-dateutil", Provided: true},
		{Kind: "python", Name: "requests"},
		{Kind: "python", Name: "jinja2"},
	}, reqs)
}
