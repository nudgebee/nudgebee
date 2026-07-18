package agents

import "testing"

// The cleanup glob and the conversation-id argument must use the same sanitized
// form the workspace uses for its temp directories — Slack session IDs contain a
// '.', which both failed the workspace's ^[a-zA-Z0-9_-]+$ validation and missed
// the sanitized directory names, leaking a full repo clone per Slack analysis.
func TestSanitizeWorkspacePathID(t *testing.T) {
	cases := map[string]string{
		"C09UAEFSTCZ-1784265269.232219":        "C09UAEFSTCZ-1784265269_232219",
		"60f3b13d-7bad-4183-bddf-19be6f4882bd": "60f3b13d-7bad-4183-bddf-19be6f4882bd", // UUIDs unchanged
		"weird id/with:chars":                  "weird_id_with_chars",
		"":                                     "",
	}
	for in, want := range cases {
		if got := sanitizeWorkspacePathID(in); got != want {
			t.Errorf("sanitizeWorkspacePathID(%q) = %q, want %q", in, got, want)
		}
	}
}
