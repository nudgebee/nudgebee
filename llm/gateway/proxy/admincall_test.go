package proxy

import "testing"

func TestIsAdminCall(t *testing.T) {
	admin := []string{
		"/cachedContents",                      // create
		"/cachedContents/abc123",               // get/delete
		"/v1beta/cachedContents",               // versioned
		"/models/gemini-2.5-flash:countTokens", // token counting
		"/v1beta/models",                       // model list (Gemini)
		"/v1beta/models/",                      // model list with trailing slash
		"/v1/models",                           // model list (OpenAI)
		"/v1/models/",                          // model list with trailing slash
	}
	inference := []string{
		"/v1/messages",                                   // Anthropic
		"/v1/chat/completions",                           // OpenAI
		"/models/gemini-2.5-flash:generateContent",       // Gemini (model in path)
		"/models/gemini-2.5-flash:streamGenerateContent", // Gemini stream
		"/v1/embeddings",
	}
	for _, p := range admin {
		if !isAdminCall(p) {
			t.Errorf("expected admin call: %q", p)
		}
	}
	for _, p := range inference {
		if isAdminCall(p) {
			t.Errorf("expected inference call (not admin): %q", p)
		}
	}
}
