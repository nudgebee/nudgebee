package bedrockclient

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetProvider(t *testing.T) {
	cases := map[string]string{
		"anthropic.claude-sonnet-4-6":                 "anthropic",
		"us.anthropic.claude-sonnet-4-6":              "anthropic",
		"us-gov.anthropic.claude-sonnet-4-6":          "anthropic",
		"eu.anthropic.claude-sonnet-4-6":              "anthropic",
		"apac.anthropic.claude-sonnet-4-6":            "anthropic",
		"jp.anthropic.claude-sonnet-4-6":              "anthropic",
		"au.anthropic.claude-sonnet-4-6":              "anthropic",
		"ca.anthropic.claude-sonnet-4-6":              "anthropic",
		"global.anthropic.claude-sonnet-4-6":          "anthropic",
		"ap.anthropic.claude-sonnet-4-6":              "anthropic",
		"sa.anthropic.claude-sonnet-4-6":              "anthropic",
		"af.anthropic.claude-sonnet-4-6":              "anthropic",
		"me.anthropic.claude-sonnet-4-6":              "anthropic",
		"us.anthropic.claude-haiku-4-5-20251001-v1:0": "anthropic",
		"apac.meta.llama3-1-70b-instruct-v1:0":        "meta",
		"arn:aws:bedrock:ap-northeast-1:123456789012:inference-profile/jp.anthropic.claude-sonnet-4-6": "anthropic",
	}
	for modelID, want := range cases {
		assert.Equal(t, want, getProvider(modelID), modelID)
	}
}
