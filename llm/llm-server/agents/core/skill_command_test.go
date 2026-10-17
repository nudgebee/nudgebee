package core

import (
	"errors"
	"strings"
	"testing"

	"nudgebee/llm/knowledge"

	"github.com/stretchr/testify/assert"

	"nudgebee/llm/security"
)

func stubSkillsForCommand(t *testing.T, skills map[string]knowledge.SkillCommand, err error) *[]string {
	t.Helper()
	orig := getSkillsForCommandFn
	t.Cleanup(func() { getSkillsForCommandFn = orig })
	var looked []string
	getSkillsForCommandFn = func(_ *security.RequestContext, _ string, names []string) (map[string]knowledge.SkillCommand, error) {
		looked = append(looked, names...)
		out := map[string]knowledge.SkillCommand{}
		for _, n := range names {
			if s, ok := skills[strings.ToLower(n)]; ok {
				out[strings.ToLower(n)] = s
			}
		}
		return out, err
	}
	return &looked
}

func TestEvalSkillCommand(t *testing.T) {
	skills := map[string]knowledge.SkillCommand{
		"pod-triage":  {ID: "kb-1", Name: "Pod triage", Data: "Check restarts, then events."},
		"db-failover": {ID: "kb-2", Name: "DB failover", Data: "Promote the replica."},
	}
	const triage = "<skill name=\"Pod triage\">\nCheck restarts, then events.\n</skill>"
	const failover = "<skill name=\"DB failover\">\nPromote the replica.\n</skill>"

	cases := []struct {
		name, query, want string
		lookedUp          []string
	}{
		{"slash name with question", "/pod-triage why is api crashing", triage + "\n\nwhy is api crashing", []string{"pod-triage"}},
		{"call alias", "/call pod-triage ns=prod", triage + "\n\nns=prod", []string{"pod-triage"}},
		{"call alias with extra spaces", "/call    pod-triage    ns=prod", triage + "\n\nns=prod", []string{"pod-triage"}},
		{"name only", "  /Pod-Triage  ", triage, []string{"Pod-Triage"}},
		{"after agent mention", "@k8s_debug /pod-triage api", "@k8s_debug " + triage + "\n\napi", []string{"pod-triage"}},
		{"middle of the message", "why is api crashing? use /pod-triage, then report", triage + "\n\nwhy is api crashing? use \"Pod triage\", then report", []string{"pod-triage"}},
		{"end of the message", "api keeps restarting /pod-triage", triage + "\n\napi keeps restarting \"Pod triage\"", []string{"pod-triage"}},
		{"two skills at the start", "/pod-triage /db-failover api is down", triage + "\n\n" + failover + "\n\napi is down", []string{"pod-triage", "db-failover"}},
		{"two skills apart", "/db-failover first, then /pod-triage.", failover + "\n\n" + triage + "\n\nfirst, then \"Pod triage\".", []string{"db-failover", "pod-triage"}},
		{"same skill twice", "/pod-triage api, and /pod-triage web", triage + "\n\napi, and \"Pod triage\" web", []string{"pod-triage", "pod-triage"}},
		{"unknown name before a skill", "/tmp is full, use /pod-triage", triage + "\n\n/tmp is full, use \"Pod triage\"", []string{"tmp", "pod-triage"}},
		{"paths are never mentions", "/var/log/syslog shows errors under /api/v1/pods", "/var/log/syslog shows errors under /api/v1/pods", nil},
		{"slash inside a word", "read and/or write a/b", "read and/or write a/b", nil},
		{"unknown name passes through", "/deploy now", "/deploy now", []string{"deploy"}},
		{"followed by a colon", "use /pod-triage: api is down", triage + "\n\nuse \"Pod triage\": api is down", []string{"pod-triage"}},
		{"punctuation after a leading skill", "/pod-triage, then report", triage + "\n\nthen report", []string{"pod-triage"}},
		{"filename is not a mention", "see /pod-triage.md for notes", "see /pod-triage.md for notes", nil},
		{"no slash", "why is api crashing", "why is api crashing", nil},
		{"bare slash", "/ hello", "/ hello", nil},
		{"call without name", "/call", "/call", []string{"call"}},
		{"at most three looked up", "/a1 /b1 /c1 /pod-triage", "/a1 /b1 /c1 /pod-triage", []string{"a1", "b1", "c1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			looked := stubSkillsForCommand(t, skills, nil)
			got, _ := evalSkillCommand(security.NewRequestContextForSuperAdmin(), NBAgentRequest{AccountId: "acc-1", Query: tc.query})
			assert.Equal(t, tc.want, got.Query)
			assert.Equal(t, tc.lookedUp, *looked)
		})
	}
}

func TestEvalSkillCommand_LookupErrorKeepsMessage(t *testing.T) {
	stubSkillsForCommand(t, nil, errors.New("db down"))
	got, _ := evalSkillCommand(security.NewRequestContextForSuperAdmin(), NBAgentRequest{AccountId: "acc-1", Query: "/pod-triage api"})
	assert.Equal(t, "/pod-triage api", got.Query)
}

func TestEvalSkillCommand_CutsLongSkill(t *testing.T) {
	stubSkillsForCommand(t, map[string]knowledge.SkillCommand{
		"big": {ID: "kb-2", Name: "Big", Data: strings.Repeat("é", knowledge.SkillCommandMaxChars+5)},
	}, nil)
	got, _ := evalSkillCommand(security.NewRequestContextForSuperAdmin(), NBAgentRequest{AccountId: "acc-1", Query: "/big go"})
	assert.Contains(t, got.Query, `[Cut at 16000 characters. Load the skill "Big" to read the rest.]`)
	assert.True(t, strings.HasSuffix(got.Query, "\n\ngo"))
}

func TestEvalSkillCommand_ReturnsLoadedSkillsOnce(t *testing.T) {
	stubSkillsForCommand(t, map[string]knowledge.SkillCommand{
		"pod-triage": {ID: "kb-1", Name: "Pod triage", Data: "x"},
	}, nil)
	_, used := evalSkillCommand(security.NewRequestContextForSuperAdmin(), NBAgentRequest{AccountId: "acc-1", Query: "/pod-triage api, then /pod-triage web /nope"})
	assert.Equal(t, []knowledge.SkillCommand{{ID: "kb-1", Name: "Pod triage", Data: "x"}}, used)
}

func TestSaveSkillCommandReferences(t *testing.T) {
	orig := saveSkillCommandReferencesFn
	t.Cleanup(func() { saveSkillCommandReferencesFn = orig })
	var saved []AgentReference
	var savedMessage string
	saveSkillCommandReferencesFn = func(_, _, messageId, _ string, refs []AgentReference) error {
		savedMessage, saved = messageId, refs
		return nil
	}
	ctx := security.NewRequestContextForSuperAdmin()

	saveSkillCommandReferences(ctx, NBAgentRequest{AccountId: "acc-1", MessageId: "msg-1"}, "conv-1",
		[]knowledge.SkillCommand{{ID: "kb-1", Name: "Pod triage"}})
	assert.Equal(t, "msg-1", savedMessage)
	assert.Equal(t, []AgentReference{{
		Type:        AgentReferenceTypeKB,
		ReferenceID: "kb-1",
		Metadata:    map[string]any{"kind": AgentReferenceKindSkill, "via": "skill_command", "name": "Pod triage"},
	}}, saved)

	saved = nil
	saveSkillCommandReferences(ctx, NBAgentRequest{AccountId: "acc-1"}, "conv-1", []knowledge.SkillCommand{{ID: "kb-1"}})
	assert.Nil(t, saved, "no message id, nothing to attach to")
}
