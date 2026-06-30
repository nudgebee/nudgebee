package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsDataRetrievalOrActionRequest(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"get pods", true},
		{"list services", true},
		{"show logs", true},
		{"give me the list of running pods in nudgebee namespace", true},
		{"can you list the pods", true},
		{"please show me the logs", true},
		{"could you please provide the list of nodes", true},
		{"check the events", true},
		{"what are the pods", true},
		{"who are you", false},
		{"hello", false},
		{"how are you", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsDataRetrievalOrActionRequest(tt.input))
		})
	}
}

func TestIsInvestigationRequestTask(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		// --- existing behavior (must stay green) ---
		{"investigate why the pod is failing", true},
		{"troubleshoot oom issue", true},
		{"is there any oom error?", true}, // oom is inherently anomalous
		{"do we have oom?", true},
		{"list pods", false},
		{"who are you", false},

		// --- strong investigation intent / anomalous states ---
		{"diagnose high memory usage on checkout-svc", true},
		{"debug the failing deployment", true},
		{"pods are in crashloopbackoff", true},
		{"the checkout page is broken", true},
		{"why is checkout-svc slow in prod", true},
		{"can you investigate the database connection drops", true},

		// --- weak nouns promoted by a problem indicator ---
		{"my pods keep restarting", true},
		{"checkout-svc is throwing 500 errors", true},
		{"latency is suddenly high on the api", true},
		{"requests are intermittently failing", true},
		// a real negative contraction promotes a weak noun (not a trailing -nt word)
		{"the deployment isn't restarting", true},
		// 5xx beyond 50x must still register as a weak signal (was \b50[0-9]\b)
		{"gateway returning 522 errors repeatedly", true},

		// --- false positives the old substring matcher got wrong ---
		{"show me health checks", false},
		// 'deployment' must not match the negative-contraction indicator via its trailing 'nt'
		{"deployment error logs", false},
		{"what's the restart policy on my deployment", false},
		{"is the error budget exhausted", false},
		{"count failed jobs", false},
		{"explain the architecture of checkout-svc", false},
		{"what is a kubernetes daemonset", false},
		{"how do i configure prometheus scraping", false},
		{"get error logs for checkout-svc", false},
		{"show me the slow query log", false},

		// --- definitional-but-causal stays an investigation ---
		{"what is causing checkout-svc to crash", true},
		{"explain why the pod crashed", true},

		// --- causal phrasing survives a retrieval-looking prefix ---
		{"show me why the pods restarted", true},

		// --- bare 'why' is not an investigation; 'why <anything>' is ---
		{"why?", false},
		{"why", false},
		{"why pod", true},
		{"why node", true},
		{"why db", true},
		{"why api", true},
		{"why app", true},
		{"why svc", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsInvestigationRequestTask(tt.input))
		})
	}
}

func TestIsConversationalQuery(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"hi", true},
		{"hello", true},
		{"who are you", true},
		{"what are you", true},
		{"what is nubi", true},
		{"help", true},
		{"get pods", false},
		{"list services", false},
		{"investigate oom", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsConversationalQuery(tt.input))
		})
	}
}

func TestMergeAccountPrompts(t *testing.T) {
	tests := []struct {
		name     string
		parts    []string
		expected string
	}{
		{"all empty", []string{"", "", ""}, ""},
		{"single non-empty", []string{"only"}, "only"},
		{"event-analysis only", []string{"event prompt", ""}, "event prompt"},
		{"gc only", []string{"", "gc body"}, "gc body"},
		{"event-analysis first then gc", []string{"event prompt", "gc body"}, "event prompt\n\ngc body"},
		{"trims whitespace fragments", []string{"  \t\n", "real"}, "real"},
		{"more than two parts compose in order", []string{"a", "b", "c"}, "a\n\nb\n\nc"},
		{"verbatim duplicate fragment is dropped", []string{"same body", "same body"}, "same body"},
		{"duplicate with surrounding whitespace is still dropped", []string{"same body", "  same body\n"}, "same body"},
		{"keeps distinct fragments even if one contains the other", []string{"prefix", "prefix and tail"}, "prefix\n\nprefix and tail"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, mergeAccountPrompts(tc.parts...))
		})
	}
}

func TestMergeAccountPrompts_PreservesPriorityForGlobalPreferencesBlock(t *testing.T) {
	// Validates that the merge output renders cleanly into the existing
	// <global_preferences> block — event-analysis prompt first, GC second.
	out := mergeAccountPrompts("event-analysis instructions", "account global context body")
	block := renderGlobalPreferencesBlock(out)
	assert.Contains(t, block, "<global_preferences>")
	assert.Contains(t, block, "event-analysis instructions")
	assert.Contains(t, block, "account global context body")
	// event-analysis must appear before the GC fragment.
	assert.Less(t,
		stringsIndex(block, "event-analysis instructions"),
		stringsIndex(block, "account global context body"),
		"event-analysis prompt must appear before the global-context body",
	)
}

func stringsIndex(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
