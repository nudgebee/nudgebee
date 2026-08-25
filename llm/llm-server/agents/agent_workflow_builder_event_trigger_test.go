package agents

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// eventTrigger builds a minimal workflow map with a single event trigger carrying the given filter.
func eventTriggerWorkflow(filter string) map[string]interface{} {
	return map[string]interface{}{
		"definition": map[string]interface{}{
			"triggers": []interface{}{
				map[string]interface{}{
					"type":   "event",
					"params": map[string]interface{}{"filter": filter},
				},
			},
		},
	}
}

func TestLintEventTriggers(t *testing.T) {
	t.Run("flags non-existent event.reason", func(t *testing.T) {
		issues := lintEventTriggers(eventTriggerWorkflow("{{ event.reason == 'OOMKilled' }}"))
		assert.NotEmpty(t, issues)
		assert.Contains(t, strings.Join(issues, "\n"), "event.reason")
	})

	t.Run("flags event.message", func(t *testing.T) {
		issues := lintEventTriggers(eventTriggerWorkflow("{{ 'OOM' in event.message }}"))
		assert.Contains(t, strings.Join(issues, "\n"), "event.message")
	})

	t.Run("flags Inputs.event used in a filter", func(t *testing.T) {
		issues := lintEventTriggers(eventTriggerWorkflow("{{ Inputs.event.priority == 'HIGH' }}"))
		assert.Contains(t, strings.Join(issues, "\n"), "Inputs.event")
	})

	t.Run("flags event.cluster compared to a UUID", func(t *testing.T) {
		issues := lintEventTriggers(eventTriggerWorkflow("{{ event.cluster == 'a2a30b02-0f67-42e5-a2ab-c658230fd798' }}"))
		assert.Contains(t, strings.Join(issues, "\n"), "cluster")
	})

	t.Run("accepts a correct severity filter", func(t *testing.T) {
		issues := lintEventTriggers(eventTriggerWorkflow("{{ event.computed_priority in ['P0','P1'] and event.nb_status == 'OPEN' }}"))
		assert.Empty(t, issues)
	})

	t.Run("does not flag free-form label keys", func(t *testing.T) {
		// event.labels.<key> captures the `labels` segment (known); the key itself is not linted.
		issues := lintEventTriggers(eventTriggerWorkflow("{{ event.labels.alertname == 'KubePodCrashLooping' }}"))
		assert.Empty(t, issues)
	})

	t.Run("ignores non-event triggers", func(t *testing.T) {
		wf := map[string]interface{}{"definition": map[string]interface{}{"triggers": []interface{}{
			map[string]interface{}{"type": "webhook", "params": map[string]interface{}{"filter": "{{ webhook_payload.reason }}"}},
		}}}
		assert.Empty(t, lintEventTriggers(wf))
	})

	t.Run("empty filter is fine", func(t *testing.T) {
		// A filter may be empty when event_type carries the match — hence the narrow rule below.
		assert.Empty(t, lintEventTriggers(eventTriggerWorkflow("")))
	})

	// An event trigger with no params at all matches nothing. This used to fall through the
	// empty-filter skip, so the lint stayed silent on precisely the shape it exists to catch
	// (#35383) — the flat trigger, whose params block never existed.
	t.Run("flags an event trigger with no params", func(t *testing.T) {
		wf := map[string]interface{}{"definition": map[string]interface{}{"triggers": []interface{}{
			map[string]interface{}{"type": "event"},
		}}}
		issues := lintEventTriggers(wf)
		assert.NotEmpty(t, issues, "an event trigger with no params can never fire")
		assert.Contains(t, strings.Join(issues, "\n"), "no params")
	})

	t.Run("flags an event trigger with an empty params block", func(t *testing.T) {
		wf := map[string]interface{}{"definition": map[string]interface{}{"triggers": []interface{}{
			map[string]interface{}{"type": "event", "params": map[string]interface{}{}},
		}}}
		assert.NotEmpty(t, lintEventTriggers(wf))
	})

	t.Run("event_type without a filter is accepted", func(t *testing.T) {
		wf := map[string]interface{}{"definition": map[string]interface{}{"triggers": []interface{}{
			map[string]interface{}{"type": "event", "params": map[string]interface{}{"event_type": "alert"}},
		}}}
		assert.Empty(t, lintEventTriggers(wf), "event_type alone is a legitimate match")
	})
}

// TestEventTriggerSchemaReference asserts the authoritative reference names the real fields and the
// guardrails the agent kept violating, so the get_event_trigger_schema tool can't silently drift.
func TestEventTriggerSchemaReference(t *testing.T) {
	ref := getEventTriggerSchemaReference()
	for _, want := range []string{
		"computed_priority", "computed_score", "nb_status",
		"NO event.reason", "llm.event_investigate", "investigation.completed",
		"cluster NAME", "event.<field>",
	} {
		assert.Contains(t, ref, want, "event-trigger reference must mention %q", want)
	}
}
