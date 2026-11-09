package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/require"
)

// Seen on dev (2026-10-06): "Get the details of Event with id …" asked in a
// k8s-dev conversation about an event on k8s-prod answered "No event found"
// after 8 lookups, all scoped to k8s-dev. An event id is unique, so the
// lookup runs on the event's own account when the caller can read it.
func TestEventLookupRunsOnTheEventsAccount(t *testing.T) {
	owner := map[string]string{"evt-prod": "prod-id", "evt-dev": "dev-id", "evt-other-tenant": ""}
	prevLookup, prevDescribe := eventOwnerLookup, describeAccount
	t.Cleanup(func() { eventOwnerLookup, describeAccount = prevLookup, prevDescribe })
	eventOwnerLookup = func(_ *security.RequestContext, id string) (string, error) { return owner[id], nil }
	describeAccount = func(id string) *core.ExecutionTarget {
		return &core.ExecutionTarget{Provider: "kubernetes", Name: map[string]string{"prod-id": "k8s-prod", "dev-id": "k8s-dev"}[id], AccountID: id}
	}
	readsBoth := core.NbToolContext{Ctx: security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"dev-id", "prod-id"}), AccountId: "dev-id", TargetAccountId: "dev-id"}
	devOnly := core.NbToolContext{Ctx: security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"dev-id"}), AccountId: "dev-id", TargetAccountId: "dev-id"}

	ranOn := ""
	lookup := func(c core.NbToolContext) (core.NBToolResponse, error) {
		ranOn = c.ForTarget().AccountId
		return core.NBToolResponse{Data: `[{"id":"x","title":"Error log spike"}]`, Status: core.NBToolResponseStatusSuccess}, nil
	}

	resp, err := onEventAccount(readsBoth, "evt-prod", lookup)
	require.NoError(t, err)
	require.Equal(t, "prod-id", ranOn, "the event's account is read")
	ran, _ := resp.AdditionalDetails[core.MetadataExecutedTarget].(map[string]any)
	require.Equal(t, "k8s-prod", ran["name"], "the step says where it ran")
	var rows []map[string]any
	require.NoError(t, json.Unmarshal([]byte(resp.Data), &rows), "the result stays valid JSON")
	require.Contains(t, rows[0][EventAccountNoteKey], `Kubernetes cluster "k8s-prod"`, "the model is told, to say so and to target follow-ups")

	resp, _ = onEventAccount(devOnly, "evt-prod", lookup)
	require.Equal(t, "dev-id", ranOn, "an account the caller cannot read is not searched")
	require.Nil(t, resp.AdditionalDetails)
	require.NotContains(t, resp.Data, "k8s-prod", "nothing reveals it exists")

	_, _ = onEventAccount(readsBoth, "evt-dev", lookup)
	require.Equal(t, "dev-id", ranOn, "an event on the conversation's account is unchanged")
	_, _ = onEventAccount(readsBoth, "evt-other-tenant", lookup)
	require.Equal(t, "dev-id", ranOn, "an unknown event is unchanged")

	named := readsBoth
	named.TargetAccountId = "prod-id"
	ranOn = ""
	_, _ = onEventAccount(named, "evt-dev", lookup)
	require.Equal(t, "prod-id", ranOn, "a call scoped to another account keeps it")
}

func TestEventLookupStaysWhenTheAccountCannotBeNamed(t *testing.T) {
	prevLookup, prevDescribe := eventOwnerLookup, describeAccount
	t.Cleanup(func() { eventOwnerLookup, describeAccount = prevLookup, prevDescribe })
	eventOwnerLookup = func(*security.RequestContext, string) (string, error) { return "prod-id", nil }
	describeAccount = func(string) *core.ExecutionTarget { return nil }
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"dev-id", "prod-id"}), AccountId: "dev-id", TargetAccountId: "dev-id"}
	ranOn := ""
	_, _ = onEventAccount(ctx, "evt", func(c core.NbToolContext) (core.NBToolResponse, error) {
		ranOn = c.ForTarget().AccountId
		return core.NBToolResponse{}, nil
	})
	require.Equal(t, "dev-id", ranOn, "a step that cannot say where it ran does not move")
}

func TestEventAccountNoteKeepsJSON(t *testing.T) {
	got := withEventAccountNote(`[{"id":"x","ts":1791294297376277000,"msg":"a <b> & c"}]`, "n")
	require.Contains(t, got, `"ts":1791294297376277000`, "large numbers keep their precision")
	require.Contains(t, got, `"msg":"a <b> & c"`, "text is not re-escaped")
	require.Contains(t, got, `"event_account_note":"n"`)
	var object map[string]any
	require.NoError(t, json.Unmarshal([]byte(withEventAccountNote(`{"a":1}`, "n")), &object))
	require.Equal(t, "n", object[EventAccountNoteKey])
	require.Equal(t, "n\nplain", withEventAccountNote("plain", "n"))
	require.NotPanics(t, func() { withEventAccountNote(`[null,{"id":"x"}]`, "n") }, "a null element is skipped")
	require.Equal(t, 1, strings.Count(withEventAccountNote(`[{"a":1},{"b":2},{"c":3}]`, "n"), EventAccountNoteKey), "an evidence list carries the note once")
}
