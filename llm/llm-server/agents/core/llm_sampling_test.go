package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

func TestModelSamplingOptions(t *testing.T) {
	apply := func(provider, model string) llms.CallOptions {
		o := llms.CallOptions{}
		// The planner's own temperature comes first, as in tryWithModel.
		for _, opt := range append([]llms.CallOption{llms.WithTemperature(0)}, modelSamplingOptions(provider, model)...) {
			opt(&o)
		}
		return o
	}

	o := apply("openai", "NVIDIA-Nemotron-3-Nano-30B-A3B-BF16-nanov3")
	assert.Equal(t, 0.6, o.Temperature)
	assert.Equal(t, 0.95, o.TopP)

	o = apply("google", "gemini-3.7-flash")
	assert.Equal(t, 0.0, o.Temperature, "no override for models without pinned sampling")
	assert.Zero(t, o.TopP)

	o = apply("custom", "nvidia/Llama-3.1-Nemotron-70B-Instruct")
	assert.Equal(t, 0.0, o.Temperature, "only Nemotron 3 carries the model-card sampling")

	for _, sibling := range []string{"nemotron-3.5-lightning", "nemotron-30b-instruct"} {
		assert.Equal(t, 0.0, apply("custom", sibling).Temperature, "%s is not validated for the override", sibling)
	}

	o = apply("openai", "nemotron-3-gpt-5-proxy")
	assert.Equal(t, SentinelOmitTemperature, o.Temperature, "an override must not put temperature back on a model that rejects it")
	assert.Zero(t, o.TopP, "no top_p for a model that rejects sampling parameters")
}
