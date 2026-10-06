package googleai

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
	"google.golang.org/genai"
)

func textChunk(text string, finish genai.FinishReason) *genai.GenerateContentResponse {
	return &genai.GenerateContentResponse{Candidates: []*genai.Candidate{{
		Content:      &genai.Content{Role: RoleModel, Parts: []*genai.Part{{Text: text}}},
		FinishReason: finish,
	}}}
}

func streamOf(chunks ...*genai.GenerateContentResponse) iter.Seq2[*genai.GenerateContentResponse, error] {
	return func(yield func(*genai.GenerateContentResponse, error) bool) {
		for _, c := range chunks {
			if !yield(c, nil) {
				return
			}
		}
	}
}

func collectOpts(stopAfter int) (*llms.CallOptions, *[]string) {
	var got []string
	return &llms.CallOptions{StreamingFunc: func(_ context.Context, b []byte) error {
		got = append(got, string(b))
		if stopAfter > 0 && len(got) >= stopAfter {
			return errors.New("caller stopped")
		}
		return nil
	}}, &got
}

func TestAccumulateStream(t *testing.T) {
	t.Parallel()

	t.Run("finished stream returns the full text", func(t *testing.T) {
		t.Parallel()
		opts, _ := collectOpts(0)
		resp, err := accumulateStream(context.Background(), "gemini-test", streamOf(
			textChunk("root cause ", ""),
			textChunk("found", genai.FinishReasonStop),
		), opts)
		require.NoError(t, err)
		assert.Equal(t, "root cause found", resp.Choices[0].Content)
		assert.Equal(t, string(genai.FinishReasonStop), resp.Choices[0].StopReason)
	})

	// 2026-10-01 Gemini 503s: streams closed after a few tokens with no finish
	// reason, and the fragment was saved as a finished answer.
	t.Run("stream cut off without a finish reason is an error", func(t *testing.T) {
		t.Parallel()
		opts, _ := collectOpts(0)
		resp, err := accumulateStream(context.Background(), "gemini-test", streamOf(
			textChunk("<final_answer>\n<thought>\nI have queried", ""),
		), opts)
		assert.Nil(t, resp)
		assert.ErrorIs(t, err, ErrStreamIncomplete)
		// The retry layer keys transient errors on this text.
		assert.Contains(t, err.Error(), "streaming error")
	})

	t.Run("chunk without content and without a finish reason is a cut-off", func(t *testing.T) {
		t.Parallel()
		opts, _ := collectOpts(0)
		_, err := accumulateStream(context.Background(), "gemini-test", streamOf(
			textChunk("partial", ""),
			&genai.GenerateContentResponse{Candidates: []*genai.Candidate{{}}},
		), opts)
		assert.ErrorIs(t, err, ErrStreamIncomplete)
	})

	t.Run("finish reason on a chunk without content still counts", func(t *testing.T) {
		t.Parallel()
		opts, _ := collectOpts(0)
		resp, err := accumulateStream(context.Background(), "gemini-test", streamOf(
			textChunk("partial", ""),
			&genai.GenerateContentResponse{Candidates: []*genai.Candidate{{FinishReason: genai.FinishReasonSafety}}},
		), opts)
		require.NoError(t, err)
		assert.Equal(t, string(genai.FinishReasonSafety), resp.Choices[0].StopReason)
	})

	t.Run("trailing unspecified finish reason does not erase an earlier one", func(t *testing.T) {
		t.Parallel()
		opts, _ := collectOpts(0)
		resp, err := accumulateStream(context.Background(), "gemini-test", streamOf(
			textChunk("done", genai.FinishReasonStop),
			textChunk("", genai.FinishReasonUnspecified),
		), opts)
		require.NoError(t, err)
		assert.Equal(t, string(genai.FinishReasonStop), resp.Choices[0].StopReason)
	})

	t.Run("blocked prompt with no candidates is not a cut-off", func(t *testing.T) {
		t.Parallel()
		opts, _ := collectOpts(0)
		_, err := accumulateStream(context.Background(), "gemini-test", streamOf(
			&genai.GenerateContentResponse{},
		), opts)
		assert.NotErrorIs(t, err, ErrStreamIncomplete)
	})

	t.Run("caller stopping the stream is not a cut-off", func(t *testing.T) {
		t.Parallel()
		opts, got := collectOpts(1)
		resp, err := accumulateStream(context.Background(), "gemini-test", streamOf(
			textChunk("first", ""),
			textChunk("second", genai.FinishReasonStop),
		), opts)
		require.NoError(t, err)
		assert.Equal(t, []string{"first"}, *got)
		assert.Equal(t, "first", resp.Choices[0].Content)
	})

	t.Run("stream error is returned as before", func(t *testing.T) {
		t.Parallel()
		opts, _ := collectOpts(0)
		boom := errors.New("Error 503, Message: This model is currently experiencing high demand")
		_, err := accumulateStream(context.Background(), "gemini-test", func(yield func(*genai.GenerateContentResponse, error) bool) {
			if !yield(textChunk("partial", ""), nil) {
				return
			}
			yield(nil, boom)
		}, opts)
		assert.ErrorIs(t, err, boom)
		assert.NotErrorIs(t, err, ErrStreamIncomplete)
	})
}
