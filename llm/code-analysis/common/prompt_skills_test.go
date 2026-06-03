package common

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// skillInjectingTemplates are the agent prompts that render the operator-skills
// block. The router is intentionally excluded — it's a pure classifier and
// receives only the query.
var skillInjectingTemplates = []string{
	"code_agent",
	"error_rca",
	"security_auditor",
	"performance_debugger",
	"code_fixer",
}

// codeFixerData mirrors the fields code_fixer.tmpl dereferences so it renders
// without a nil-map panic; other agents only need the common keys.
func templateData(name, skills string) map[string]any {
	data := map[string]any{
		"ContextInfo":   "repo guidance here",
		"OriginalQuery": "why does the build fail?",
		"Mode":          "fix",
		"IsExploreMode": false,
		"IsFixMode":     true,
		"Skills":        skills,
	}
	if name == "code_fixer" {
		data["AuditFindings"] = map[string]any{}
		data["InvestigationHistory"] = ""
		data["BuildConfig"] = nil
		data["BuildVerifyEnabled"] = false
	}
	return data
}

func TestSkillsInjectedWhenPresent(t *testing.T) {
	const marker = "NB-TEST-SKILL: always open PRs against the develop branch"
	skills := "<skills><skill name=\"create-pr\">" + marker + "</skill></skills>"

	loader := NewPromptLoader()
	for _, name := range skillInjectingTemplates {
		out, err := loader.LoadPrompt(name, templateData(name, skills))
		require.NoError(t, err, "template %s should render", name)
		assert.Contains(t, out, "<operator-skills>", "template %s should wrap forwarded skills", name)
		assert.Contains(t, out, marker, "template %s should inline the skill body", name)
	}
}

func TestSkillsOmittedWhenEmpty(t *testing.T) {
	loader := NewPromptLoader()
	for _, name := range skillInjectingTemplates {
		out, err := loader.LoadPrompt(name, templateData(name, ""))
		require.NoError(t, err, "template %s should render", name)
		assert.False(t, strings.Contains(out, "<operator-skills>"),
			"template %s must not emit an empty skills block", name)
	}
}
