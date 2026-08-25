package prompts

import (
	"context"
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Template variables are found in two passes: templateActionRe grabs whole {{...}}
// actions, then templateVarRe finds every dot-prefixed identifier inside one. A single
// pattern anchored to the start of the action cannot see a variable behind a function
// call, and that was not hypothetical — {{if not .shell_tool_enabled}} in
// response_formatter meant shell_tool_enabled went unchecked for undeclared use.
// The leading (^|[^a-zA-Z0-9_.]) is what keeps the second pass honest. Requiring the
// dot to follow something that cannot end an identifier excludes decimals — {{if gt
// .score 0.95}} would otherwise yield "95" — and stops a dotted path from reporting its
// own tail, since the "." in {{.Config.Enabled}} is preceded by "g" and only ".Config"
// is a variable this file must declare. Go's regexp has no lookbehind, hence the
// capture group and match[2].
var (
	templateActionRe = regexp.MustCompile(`\{\{[^}]*\}\}`)
	templateVarRe    = regexp.MustCompile(`(^|[^a-zA-Z0-9_.])\.([a-zA-Z0-9_]+)`)
)

// templateVarsIn returns every distinct top-level variable referenced by a prompt body.
func templateVarsIn(body string) []string {
	var vars []string
	seen := map[string]bool{}
	for _, action := range templateActionRe.FindAllString(body, -1) {
		for _, match := range templateVarRe.FindAllStringSubmatch(action, -1) {
			if name := match[2]; !seen[name] {
				seen[name] = true
				vars = append(vars, name)
			}
		}
	}
	return vars
}

// TestTemplateVarsIn covers the extractor itself. It is the check the whole prompt
// catalog is validated against, so a hole here silently weakens every other assertion —
// which is exactly how {{if not .shell_tool_enabled}} went undeclared until the
// single-pattern version was replaced.
func TestTemplateVarsIn(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want []string
	}{
		{"bare", "{{.input}}", []string{"input"}},
		{"if prefix", "{{if .remediation_enabled}}x{{end}}", []string{"remediation_enabled"}},
		{"behind a function", "{{if not .shell_tool_enabled}}x{{end}}", []string{"shell_tool_enabled"}},
		{"comparison", `{{if eq .question_type "investigation"}}x{{end}}`, []string{"question_type"}},
		{"multiple in one action", "{{and .a .b}}", []string{"a", "b"}},
		{"trim markers", "{{- .trimmed -}}", []string{"trimmed"}},
		{"decimal is not a variable", "{{if gt .score 0.95}}x{{end}}", []string{"score"}},
		{"dotted path reports only its root", "{{.Config.Enabled}}", []string{"Config"}},
		{"include directives are not variables", "{{@include _fragments/kg_usage}}", nil},
		{"text outside an action is ignored", "a period. not a var {{.real}}", []string{"real"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.ElementsMatch(t, tc.want, templateVarsIn(tc.body))
		})
	}
}

// walkPromptFiles visits every .yaml prompt file in the embedded FS.
func walkPromptFiles(t *testing.T, visit func(promptPath string, promptFile *PromptFile)) {
	t.Helper()

	err := fs.WalkDir(embeddedFS, ".", func(promptPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(promptPath, ".yaml") {
			return nil
		}

		data, readErr := fs.ReadFile(embeddedFS, promptPath)
		require.NoError(t, readErr, "%s: unreadable", promptPath)

		promptFile, parseErr := ParsePromptFile(data)
		require.NoError(t, parseErr, "%s: does not satisfy the prompt schema", promptPath)

		visit(promptPath, promptFile)
		return nil
	})
	require.NoError(t, err)
}

// TestPromptFiles_IdentityMatchesPath asserts the checked redundancy: `name` must
// equal the filename stem and `category` the parent directory. Identity comes from
// the path, so a file whose fields disagree with where it lives would be resolvable
// under one name and self-describe as another.
func TestPromptFiles_IdentityMatchesPath(t *testing.T) {
	walkPromptFiles(t, func(promptPath string, promptFile *PromptFile) {
		wantName := strings.TrimSuffix(path.Base(promptPath), ".yaml")
		assert.Equal(t, wantName, promptFile.Name,
			"%s: name does not match filename stem", promptPath)

		// Fragments live beside the category directories (e.g. _fragments/, _persona/)
		// and are include-only, so they carry no category.
		parent := path.Base(path.Dir(promptPath))
		if strings.HasPrefix(parent, "_") {
			assert.Empty(t, promptFile.Category,
				"%s: fragments must not declare a category", promptPath)
			return
		}
		assert.Equal(t, PromptCategory(parent), promptFile.Category,
			"%s: category does not match parent directory", promptPath)
	})
}

// TestPromptFiles_TemplateVarsDeclared asserts every {{.var}} in a body is declared
// in inputs or runtime_inputs. This is what replaces the missingkey=zero fallback:
// an undeclared variable is a build failure rather than a silently empty render.
// Includes are resolved before extracting, so a variable that arrives via a fragment is
// held to the same rule as one written inline. A fragment inherits the version of the
// prompt including it and has no inputs block of its own, which makes the including
// prompt responsible for declaring whatever its fragments reference.
func TestPromptFiles_TemplateVarsDeclared(t *testing.T) {
	loader := &PromptLoader{fs: embeddedFS, cache: NewPromptCache(1 * time.Hour)}

	walkPromptFiles(t, func(promptPath string, promptFile *PromptFile) {
		body := promptFile.Body
		if len(promptFile.Includes) > 0 {
			// Paths are {provider}/{version}/{category}/{name}.yaml; processIncludes
			// needs the first two segments. SplitN caps the work and the guard turns a
			// malformed path into a readable failure rather than an index panic.
			parts := strings.SplitN(promptPath, "/", 3)
			require.Len(t, parts, 3, "%s: expected {provider}/{version}/... path", promptPath)
			resolved, err := loader.processIncludes(body, parts[0], parts[1], 0)
			require.NoError(t, err, "%s: includes must resolve", promptPath)
			body = resolved
		}

		for _, name := range templateVarsIn(body) {
			if _, declared := promptFile.Inputs[name]; declared {
				continue
			}
			if slices.Contains(promptFile.RuntimeInputs, name) {
				continue
			}
			t.Errorf("%s: template var {{.%s}} is not declared in inputs or runtime_inputs",
				promptPath, name)
		}
	})
}

// TestPromptFiles_IncludesManifestMatchesBody asserts the `includes` manifest and the
// {{@include ...}} markers in the body describe the same set. The manifest lets tooling
// see a prompt's dependencies without parsing the body; a drifting pair would make it lie.
func TestPromptFiles_IncludesManifestMatchesBody(t *testing.T) {
	walkPromptFiles(t, func(promptPath string, promptFile *PromptFile) {
		var inBody []string
		for _, match := range includeRegex.FindAllStringSubmatch(promptFile.Body, -1) {
			inBody = append(inBody, strings.TrimSpace(match[1]))
		}

		declared := append([]string(nil), promptFile.Includes...)
		slices.Sort(declared)
		slices.Sort(inBody)

		assert.Equal(t, declared, inBody,
			"%s: includes manifest and {{@include}} markers disagree", promptPath)
	})
}

// TestPromptFiles_NamesUniqueAcrossCategories asserts prompt names are globally unique.
// Resolution takes a bare name and derives the category, so two categories claiming one
// name would make the lookup ambiguous.
func TestPromptFiles_NamesUniqueAcrossCategories(t *testing.T) {
	// Versions and provider overrides of one prompt legitimately share a name, so
	// collect the distinct categories each name is claimed by rather than counting files.
	categoriesByName := map[string]map[PromptCategory]bool{}

	walkPromptFiles(t, func(promptPath string, promptFile *PromptFile) {
		if promptFile.Category == "" {
			return // fragments are addressed by path, not by name
		}
		if categoriesByName[promptFile.Name] == nil {
			categoriesByName[promptFile.Name] = map[PromptCategory]bool{}
		}
		categoriesByName[promptFile.Name][promptFile.Category] = true
	})

	for name, categories := range categoriesByName {
		assert.Len(t, categories, 1,
			"prompt name %q is claimed by more than one category: %v", name, categories)
	}
}

// TestPromptFiles_NamingConvention asserts names convert cleanly to a Go identifier,
// which the generated constants will depend on.
func TestPromptFiles_NamingConvention(t *testing.T) {
	nameRe := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

	walkPromptFiles(t, func(promptPath string, promptFile *PromptFile) {
		assert.Regexp(t, nameRe, promptFile.Name,
			"%s: name must be lowercase snake_case", promptPath)
		if promptFile.Category == CategoryAgents {
			assert.False(t, strings.HasPrefix(promptFile.Name, "agent_"),
				"%s: drop the agent_ prefix — the agents/ directory already carries it", promptPath)
		}
	})
}

// TestAllRegisteredPromptsResolve is the build-time counterpart of MustResolveAll:
// every prompt in promptCategories must have a file. There is no fallback tree left,
// so a missing file is a hard failure rather than a silent degradation.
func TestAllRegisteredPromptsResolve(t *testing.T) {
	// Restore the singleton: this package's tests share process state, so leaving a
	// test loader installed makes later tests depend on execution order.
	oldLoader := GetLoader()
	t.Cleanup(func() { SetGlobalLoaderForTesting(oldLoader) })
	SetGlobalLoaderForTesting(NewLoaderForTesting())
	require.NoError(t, MustResolveAll())
}

// TestRegisteredPromptsCoverTree asserts the registry and the embedded tree agree:
// every non-fragment .yaml under default/v1 is registered, so an orphaned prompt file
// cannot sit in the tree waiting to be picked up by a future mapping edit.
func TestRegisteredPromptsCoverTree(t *testing.T) {
	walkPromptFiles(t, func(promptPath string, promptFile *PromptFile) {
		if !strings.HasPrefix(promptPath, "default/v1/") {
			return
		}
		// Fragments (_persona/, _fragments/) are include-only: they are spliced into
		// other prompts by path and never resolved by name, so they carry no registry
		// entry by design.
		if strings.HasPrefix(path.Base(path.Dir(promptPath)), "_") {
			return
		}
		if _, registered := promptCategories[promptFile.Name]; !registered {
			t.Errorf("%s: prompt file is not registered in promptCategories", promptPath)
		}
	})
}

// TestGetPromptStrict_ServesMigratedYAML is the end-to-end check for the YAML path.
// The migrated prompt has no legacy .txt left to fall back to, so this failing means
// the tool is broken outright — which is the intended trade for silent degradation.
func TestGetPromptStrict_ServesMigratedYAML(t *testing.T) {
	// Restore the singleton: this package's tests share process state, so leaving a
	// test loader installed makes later tests depend on execution order.
	oldLoader := GetLoader()
	t.Cleanup(func() { SetGlobalLoaderForTesting(oldLoader) })
	SetGlobalLoaderForTesting(NewLoaderForTesting())

	got, err := GetPromptStrict(context.Background(), PromptRemediationGenerate, "")

	require.NoError(t, err)
	require.NotEmpty(t, got)
	assert.Contains(t, got, "You are a helpful technical expert assistant.")
	assert.NotContains(t, got, "body: |-", "raw YAML leaked into the rendered prompt")
}

// TestGetPromptStrict_ErrorsOnUnknownModule asserts the strict accessor reports a
// missing prompt instead of returning "" for a caller to paper over.
func TestGetPromptStrict_ErrorsOnUnknownModule(t *testing.T) {
	// Restore the singleton: this package's tests share process state, so leaving a
	// test loader installed makes later tests depend on execution order.
	oldLoader := GetLoader()
	t.Cleanup(func() { SetGlobalLoaderForTesting(oldLoader) })
	SetGlobalLoaderForTesting(NewLoaderForTesting())

	_, err := GetPromptStrict(context.Background(), "no_such_prompt", "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not registered")
}

// TestMigratedPrompts_MatchLegacyBody guards a conversion in flight: while a prompt
// has both a YAML file and its prompts_repo .txt, the bodies must be byte-identical so
// the migration cannot quietly reword it. An entry lives here only for the duration of
// its conversion — deleting the legacy .txt is the last step, and removes the entry too.
func TestMigratedPrompts_MatchLegacyBody(t *testing.T) {
	migrated := map[string]string{}

	for yamlPath, legacyPath := range migrated {
		data, err := fs.ReadFile(embeddedFS, yamlPath)
		require.NoError(t, err, "%s: unreadable", yamlPath)

		promptFile, err := ParsePromptFile(data)
		require.NoError(t, err, "%s: does not satisfy the prompt schema", yamlPath)

		legacy, err := os.ReadFile(legacyPath)
		require.NoError(t, err, "%s: unreadable", legacyPath)

		assert.Equal(t, string(legacy), promptFile.Body,
			"%s: body drifted from %s", yamlPath, legacyPath)
	}
}

// TestMalformedOverrideFallsThroughToBaseline pins the invariant that makes the
// plain string-returning GetPrompt safe: a registered prompt never resolves to "".
// A malformed provider- or version-specific override is logged and skipped, and
// resolution continues to default/v1 — the baseline MustResolveAll validated at
// startup. Returning "" here would hand an agent an empty system prompt.
func TestMalformedOverrideFallsThroughToBaseline(t *testing.T) {
	base, err := fs.Sub(embeddedFS, ".")
	require.NoError(t, err)

	// default/v1 is real; the googleai override is deliberately unparseable.
	overlay := fstest.MapFS{
		"googleai/v1/tools/remediation_generate.yaml": &fstest.MapFile{
			Data: []byte("apiVersion: nudgebee.dev/prompt/v1\nname: remediation_generate\n  broken: [[[\n"),
		},
	}

	l := &PromptLoader{cache: NewPromptCache(time.Hour), fs: mergedFS{base: base, over: overlay}}

	got, err := l.loadPromptFile("remediation_generate", CategoryTools, "googleai", "v1")
	require.NoError(t, err, "a malformed override must not fail the load")
	assert.Contains(t, got, "You are a helpful technical expert assistant.",
		"should have fallen through to the validated default/v1 baseline")
}

// mergedFS serves over first, then base — enough to stage a bad override in a test.
type mergedFS struct{ base, over fs.FS }

func (m mergedFS) Open(name string) (fs.File, error) {
	if f, err := m.over.Open(name); err == nil {
		return f, nil
	}
	return m.base.Open(name)
}

// positionalVerbCounts pins how many printf verbs each prompt body carries. Callers pass
// these positionally through GetPrompt's variadic args, so the count is a contract between
// a prompt file and its Go call site that neither the compiler nor a rendering test can
// check: a mismatch produces %!s(MISSING) or %!(EXTRA ...) inside a prompt that is then
// sent to the model, and everything still builds and passes.
//
// Editing a body so its verb count changes fails this test on purpose — the fix is to
// update the call site in the same change, then update this map.
//
// kg_usage is deliberately absent despite containing a literal "%db%": it is a SQL LIKE
// wildcard in prose, and its call site passes no args, so no Sprintf ever runs over it.
var positionalVerbCounts = map[string]int{
	"v1/tools/config_auto_selection.yaml":             7,
	"v1/utilities/agent_llm.yaml":                     3,
	"v1/utilities/evaluator_query_response.yaml":      2,
	"v1/utilities/evaluator_tool_calls.yaml":          3,
	"v1/utilities/event_investigation.yaml":           10,
	"v1/utilities/log_analysis_file_extractor.yaml":   1,
	"v1/utilities/scratchpad_context_summarizer.yaml": 3,
	"v1/utilities/scratchpad_summarizer.yaml":         4,
}

var printfVerbRe = regexp.MustCompile(`%[sdvqt]`)

func TestPromptFiles_PositionalVerbCountsArePinned(t *testing.T) {
	seen := map[string]bool{}

	walkPromptFiles(t, func(promptPath string, promptFile *PromptFile) {
		key := strings.TrimPrefix(promptPath, "default/")
		got := len(printfVerbRe.FindAllString(promptFile.Body, -1))
		want, pinned := positionalVerbCounts[key]

		switch {
		case pinned:
			seen[key] = true
			assert.Equal(t, want, got,
				"%s: printf verb count changed (pinned %d, found %d) — update the Go call site's args, then this pin",
				promptPath, want, got)
		case got > 0 && key != "v1/_fragments/kg_usage.yaml":
			t.Errorf("%s: has %d printf verb(s) but is not pinned in positionalVerbCounts; "+
				"add it with the number of args its call site passes", promptPath, got)
		}
	})

	for key := range positionalVerbCounts {
		assert.True(t, seen[key], "%s: pinned in positionalVerbCounts but no such prompt file", key)
	}
}
