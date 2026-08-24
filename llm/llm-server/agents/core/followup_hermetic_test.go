package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFollowupRequestForMissingInformation covers the refine-followup path
// hermetically: FollowupRequestForMissingInformation only calls
// GenerateAndTrackLLMContent (JSON mode) and then parses the
// {"followup_questions": [...]} response, so the fake-LLM seam lets us assert
// the request/parse contract without a live provider. (Replaces the
// TEST_ACCOUNT-gated TestRefineFollowup2 e2e test, which only asserted the
// question was non-empty against a real model.)
func TestFollowupRequestForMissingInformation(t *testing.T) {
	agent := LLMAgent{}

	cases := []struct {
		name         string
		response     string
		fakeErr      bool
		wantErr      bool
		wantQuestion string
	}{
		{
			name:         "single question is extracted",
			response:     `{"followup_questions":["Which namespace is affected?"]}`,
			wantQuestion: "Which namespace is affected?",
		},
		{
			name:         "first question is returned when several are present",
			response:     `{"followup_questions":["First?","Second?"]}`,
			wantQuestion: "First?",
		},
		{
			name:         "backtick-fenced JSON is trimmed and parsed",
			response:     "```" + `{"followup_questions":["Fenced?"]}` + "```",
			wantQuestion: "Fenced?",
		},
		{
			name:         "empty question array yields no followup",
			response:     `{"followup_questions":[]}`,
			wantQuestion: "",
		},
		{
			name:         "missing followup_questions key yields no followup",
			response:     `{"unrelated":true}`,
			wantQuestion: "",
		},
		{
			name:         "empty model content yields no followup",
			response:     "",
			wantQuestion: "",
		},
		{
			name:     "malformed JSON surfaces an error",
			response: `{not valid json`,
			wantErr:  true,
		},
		{
			name:         "LLM error is swallowed into an empty followup",
			response:     "irrelevant",
			fakeErr:      true,
			wantQuestion: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeLLMModel{response: tc.response}
			if tc.fakeErr {
				fake.err = assert.AnError
			}
			withFakeLLMModel(t, fake)

			query := NBAgentRequest{
				Query:          "my pods keep failing and I'm not sure why",
				UserId:         "user-1",
				AccountId:      "acct-1",
				ConversationId: "conv-1",
				MessageId:      "msg-1",
			}
			fr, err := FollowupRequestForMissingInformation(llmOverrideContext(), query, agent)

			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.wantQuestion, fr.Question)
			if tc.wantQuestion != "" {
				assert.Equal(t, FollowupTypeText, fr.FollowupType)
				assert.Equal(t, agent.GetName(), fr.AgentName)
			}
		})
	}
}
