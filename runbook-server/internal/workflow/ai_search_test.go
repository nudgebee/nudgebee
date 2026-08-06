package workflow

import (
	"errors"
	"testing"
	"unicode/utf8"

	"nudgebee/runbook/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func aiCandidate(id, name, llmDescription string) model.AIInvocableWorkflow {
	return model.AIInvocableWorkflow{
		ID:   id,
		Name: name,
		Definition: model.WorkflowDefinition{
			LLMDescription: llmDescription,
			Triggers:       []model.Trigger{{Type: model.WorkflowTriggerManual}},
		},
	}
}

func rankedIDs(results []model.AIInvocableWorkflow) []string {
	ids := make([]string, 0, len(results))
	for _, r := range results {
		ids = append(ids, r.ID)
	}
	return ids
}

func TestRankAIWorkflowsFindsBySymptomNotName(t *testing.T) {
	// The whole point of the feature: the user describes a symptom and never
	// names the automation. An opaque name like "prod-restart-v2" must still be
	// found through what its description says it does.
	candidates := []model.AIInvocableWorkflow{
		aiCandidate("wf-1", "prod-restart-v2",
			"Restarts the crashlooping payment consumers and drains the stuck queue."),
		aiCandidate("wf-2", "nightly-report", "Emails the weekly cost report to finance."),
	}

	got := rankAIWorkflows(candidates, "payment pods are crashlooping", 5)
	require.Len(t, got, 1, "the unrelated report workflow must not match")
	assert.Equal(t, "wf-1", got[0].ID)
}

func TestRankAIWorkflowsNameOutranksDescriptionProse(t *testing.T) {
	candidates := []model.AIInvocableWorkflow{
		// Mentions the word once, incidentally, in prose.
		aiCandidate("prose", "generic-cleanup", "Cleans up after a crashloop investigation."),
		// Carries it in the name — what an author picks deliberately.
		aiCandidate("name", "crashloop-restart", "Restarts consumers."),
	}

	got := rankAIWorkflows(candidates, "crashloop", 5)
	require.Len(t, got, 2)
	assert.Equal(t, "name", got[0].ID, "a name hit should beat an incidental prose mention")
}

func TestRankAIWorkflowsDropsNonMatches(t *testing.T) {
	// Returning an unrelated automation just because it is the only one on offer
	// invites the model to run something wrong.
	candidates := []model.AIInvocableWorkflow{
		aiCandidate("wf-1", "nightly-report", "Emails the weekly cost report."),
	}

	assert.Empty(t, rankAIWorkflows(candidates, "kubernetes pod eviction", 5))
}

func TestRankAIWorkflowsEmptyQueryListsEverything(t *testing.T) {
	// "What can you run for me?" is a legitimate question.
	candidates := []model.AIInvocableWorkflow{
		aiCandidate("wf-1", "restart-consumers", "Restarts consumers."),
		aiCandidate("wf-2", "nightly-report", "Emails the report."),
	}

	got := rankAIWorkflows(candidates, "   ", 5)
	assert.Len(t, got, 2)
}

func TestRankAIWorkflowsRespectsLimit(t *testing.T) {
	candidates := []model.AIInvocableWorkflow{
		aiCandidate("a", "restart-one", "Restarts things."),
		aiCandidate("b", "restart-two", "Restarts things."),
		aiCandidate("c", "restart-three", "Restarts things."),
	}

	assert.Len(t, rankAIWorkflows(candidates, "restart", 2), 2)
	assert.Len(t, rankAIWorkflows(candidates, "", 2), 2)
}

func TestRankAIWorkflowsIsDeterministicOnTies(t *testing.T) {
	// Equal scores must not come back in whatever order the database produced,
	// or the model's choice would vary between identical questions.
	candidates := []model.AIInvocableWorkflow{
		aiCandidate("z", "zebra-restart", "Restarts."),
		aiCandidate("a", "alpha-restart", "Restarts."),
		aiCandidate("m", "middle-restart", "Restarts."),
	}

	first := rankedIDs(rankAIWorkflows(candidates, "restart", 5))
	for i := 0; i < 5; i++ {
		assert.Equal(t, first, rankedIDs(rankAIWorkflows(candidates, "restart", 5)))
	}
	// alpha-restart / middle-restart / zebra-restart, hence ids a, m, z.
	assert.Equal(t, []string{"a", "m", "z"}, first, "ties break by name ascending")
}

func TestTokenizeNormalizesSeparatorsAndNoise(t *testing.T) {
	// "payment-service" and "payment service" must tokenize alike, so an author's
	// punctuation choice does not decide whether their automation is findable.
	assert.Equal(t, tokenize("payment-service"), tokenize("payment service"))

	// Single characters are noise, and repeats should not let one word count twice.
	assert.Equal(t, []string{"pod", "restart"}, tokenize("a pod, restart! pod"))
	assert.Empty(t, tokenize("   "))
}

func TestScoreAIWorkflowCountsEachQueryTokenOncePerField(t *testing.T) {
	// A description that repeats a word must not outrank a genuinely better match.
	repeated := aiCandidate("repeat", "generic", "restart restart restart restart restart")
	named := aiCandidate("named", "restart-consumers", "Handles consumers.")

	got := rankAIWorkflows([]model.AIInvocableWorkflow{repeated, named}, "restart", 5)
	require.Len(t, got, 2)
	assert.Equal(t, "named", got[0].ID, "keyword stuffing a description must not win")
}

func TestSearchAIInvocableWorkflowsGating(t *testing.T) {
	t.Run("returns nothing when the feature flag is off", func(t *testing.T) {
		// Without the flag the AI must not even learn which automations exist.
		stubFeatureFlag(t, false, nil)
		store := new(MockWorkflowStore)
		s := &Service{store: store}

		got, err := s.SearchAIInvocableWorkflows(aiRequestContext(true), "test-account", "restart", 0)
		require.NoError(t, err)
		assert.Empty(t, got.Workflows)
		store.AssertNotCalled(t, "ListAIInvocableWorkflows", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("fails closed when the flag lookup errors", func(t *testing.T) {
		stubFeatureFlag(t, true, errors.New("metastore unreachable"))
		store := new(MockWorkflowStore)
		s := &Service{store: store}

		got, err := s.SearchAIInvocableWorkflows(aiRequestContext(true), "test-account", "restart", 0)
		require.NoError(t, err)
		assert.Empty(t, got.Workflows)
	})

	t.Run("hides automations the run-time gate would refuse", func(t *testing.T) {
		// Surfacing something that cannot actually be run wastes a turn and makes
		// the assistant look broken, so search applies the same eligibility rules.
		stubFeatureFlag(t, true, nil)
		noManual := aiCandidate("no-manual", "scheduled-cleanup", "Cleans up nightly.")
		noManual.Definition.Triggers = []model.Trigger{{Type: model.WorkflowTriggerSchedule}}
		noDescription := aiCandidate("no-desc", "restart-thing", "")
		good := aiCandidate("good", "restart-consumers", "Restarts the consumers.")

		store := new(MockWorkflowStore)
		store.On("ListAIInvocableWorkflows", mock.Anything, mock.Anything, mock.Anything).
			Return([]model.AIInvocableWorkflow{noManual, noDescription, good}, nil)
		s := &Service{store: store}

		got, err := s.SearchAIInvocableWorkflows(aiRequestContext(true), "test-account", "restart", 0)
		require.NoError(t, err)
		require.Len(t, got.Workflows, 1)
		assert.Equal(t, "good", got.Workflows[0].ID)
		assert.Equal(t, 1, got.TotalInvocable, "only eligible automations are counted")
	})

	t.Run("surfaces inputs so the model can fill parameters without another call", func(t *testing.T) {
		stubFeatureFlag(t, true, nil)
		c := aiCandidate("wf-1", "restart-consumers", "Restarts the consumers.")
		c.Definition.Inputs = []model.Input{{ID: "namespace", Type: "string", Required: true}}

		store := new(MockWorkflowStore)
		store.On("ListAIInvocableWorkflows", mock.Anything, mock.Anything, mock.Anything).
			Return([]model.AIInvocableWorkflow{c}, nil)
		s := &Service{store: store}

		got, err := s.SearchAIInvocableWorkflows(aiRequestContext(true), "test-account", "restart", 0)
		require.NoError(t, err)
		require.Len(t, got.Workflows, 1)
		require.Len(t, got.Workflows[0].Inputs, 1)
		assert.Equal(t, "namespace", got.Workflows[0].Inputs[0].ID)
	})
}

func TestTokenizeStemsSingularAndPlural(t *testing.T) {
	// The exact near-misses found in end-to-end testing: a query one letter off
	// returned nothing at all, because non-matches are dropped.
	for _, pair := range [][2]string{
		{"payment", "payments"},
		{"pod", "pods"},
		{"restart", "restarting"},
		{"crashloop", "crashloops"},
	} {
		t.Run(pair[0]+"/"+pair[1], func(t *testing.T) {
			assert.Equal(t, tokenize(pair[0]), tokenize(pair[1]))
		})
	}

	// Short words are left alone — trimming "aws" to "aw" would help nobody.
	assert.Equal(t, []string{"aws"}, tokenize("aws"))
	// "es" is deliberately not stripped: it would split -e words from their
	// plurals ("queue" vs "queu") instead of joining them.
	assert.Equal(t, tokenize("queue"), tokenize("queues"))
}

func TestRankAIWorkflowsMatchesNearMissQueries(t *testing.T) {
	candidates := []model.AIInvocableWorkflow{
		aiCandidate("wf-1", "restart-payment-consumers",
			"Restarts the payment consumers when their pods crashloop."),
	}

	for _, query := range []string{"payments", "payment", "pods", "pod", "restarting", "crashloops"} {
		t.Run(query, func(t *testing.T) {
			got := rankAIWorkflows(candidates, query, 5)
			assert.Len(t, got, 1, "query %q should still find the automation", query)
		})
	}

	// Stemming must not turn unrelated words into matches.
	assert.Empty(t, rankAIWorkflows(candidates, "billing invoice", 5))
}

// TestRankAIWorkflowsMatchesRelatedWordForms covers the pairs stemming alone
// cannot bring together, because the two words reduce to different forms:
// "scaling" loses its "ing" while "scale" keeps its "e". Every row here was a
// miss before fieldScore gained its prefix pass, and each one is a word an
// operator would plausibly type at an automation an author plausibly named.
func TestRankAIWorkflowsMatchesRelatedWordForms(t *testing.T) {
	candidate := aiCandidate("wf-1", "capacity helper",
		"Adjusts capacity: scale, queue, ingress, process, rabbitmq.")

	for _, query := range []string{
		"scale", "scales", "scaling", // -e vs -ing, the reported case
		"queue", "queues", "queuing",
		"ingress", "ingresses", // query longer than the indexed word
		"process", "processes", "processing",
		"rabbit mq", // two tokens against one written closed-up
	} {
		t.Run(query, func(t *testing.T) {
			assert.Equal(t, []string{"wf-1"},
				rankedIDs(rankAIWorkflows([]model.AIInvocableWorkflow{candidate}, query, 5)))
		})
	}

	// The prefix pass has to stay narrow enough not to match on a shared opening
	// syllable — short tokens are where that goes wrong.
	for _, query := range []string{"awesome", "scalar", "ingot"} {
		t.Run("no match: "+query, func(t *testing.T) {
			assert.Empty(t, rankAIWorkflows([]model.AIInvocableWorkflow{
				aiCandidate("wf-1", "aws helper", "Runs on AWS. Handles aws and ingest."),
			}, query, 5))
		})
	}
}

// An exact match is the stronger signal and must always win, or a prefix hit in
// a description could outrank the automation the user actually named.
func TestRankAIWorkflowsPrefersExactOverPrefix(t *testing.T) {
	exact := aiCandidate("wf-exact", "beta", "Handles scale events.")
	prefix := aiCandidate("wf-prefix", "alpha", "Handles scaleway provisioning.")

	got := rankAIWorkflows([]model.AIInvocableWorkflow{prefix, exact}, "scale", 5)
	assert.Equal(t, []string{"wf-exact", "wf-prefix"}, rankedIDs(got))
}

// TestRankAIWorkflowsMatchesShortVerbForms covers the bases English doubles the
// final consonant on before "ing". Those strip to a four-letter stem against a
// three-letter base ("running" -> "runn" vs "run"), which the prefix pass cannot
// rescue either since it needs four shared characters — so they were outright
// misses over vocabulary this feature exists to search.
func TestRankAIWorkflowsMatchesShortVerbForms(t *testing.T) {
	candidate := aiCandidate("wf-1", "pod helper",
		"Keeps pods healthy: run, stop, get, pass.")

	for _, query := range []string{
		"run", "running", "stop", "stopping", "get", "getting", "pass", "passing",
	} {
		t.Run(query, func(t *testing.T) {
			assert.Equal(t, []string{"wf-1"},
				rankedIDs(rankAIWorkflows([]model.AIInvocableWorkflow{candidate}, query, 5)))
		})
	}
}

// The collapse runs over indexed text as well as queries, which is what keeps
// words whose base genuinely ends in a doubled consonant matching each other —
// both sides reduce to the same form rather than only one of them.
func TestRankAIWorkflowsKeepsDoubledBaseWordsMatching(t *testing.T) {
	candidate := aiCandidate("wf-1", "node helper",
		"Cleans up nodes: kill, poll, shell.")

	for _, query := range []string{
		"kill", "killing", "poll", "polling", "shell", "shells",
	} {
		t.Run(query, func(t *testing.T) {
			assert.Equal(t, []string{"wf-1"},
				rankedIDs(rankAIWorkflows([]model.AIInvocableWorkflow{candidate}, query, 5)))
		})
	}
}

// TestTokenizeKeepsNonASCIIIntact guards the doubled-consonant collapse, which
// indexes and slices bytes rather than runes. A multi-byte character can end in
// two identical continuation bytes — "充" is E5 85 85 — so an unrestricted rule
// would lop one off and leave invalid UTF-8 in a token that flows into a JSON
// response.
func TestTokenizeKeepsNonASCIIIntact(t *testing.T) {
	for _, in := range []string{"充", "namespace充", "café", "restart"} {
		t.Run(in, func(t *testing.T) {
			for _, token := range tokenize(in) {
				assert.True(t, utf8.ValidString(token), "tokenizing %q produced invalid UTF-8: % x", in, token)
			}
		})
	}
}
