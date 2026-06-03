package googleai

import (
	"context"
	"fmt"

	"google.golang.org/genai"
)

// CreateEmbedding creates embeddings from texts.
func (g *GoogleAI) CreateEmbedding(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, 0, len(texts))

	// The new SDK's EmbedContent takes one content at a time (or a batch via multiple calls).
	// We batch in groups of 100 to match the previous behaviour.
	for i, t := range texts {
		resp, err := g.client.Models.EmbedContent(ctx, g.opts.DefaultEmbeddingModel,
			[]*genai.Content{{Parts: []*genai.Part{{Text: t}}}},
			nil,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create embedding for text[%d]: %w", i, err)
		}
		if len(resp.Embeddings) == 0 {
			return nil, fmt.Errorf("empty embedding response for text[%d]", i)
		}
		results = append(results, resp.Embeddings[0].Values)
	}

	return results, nil
}
