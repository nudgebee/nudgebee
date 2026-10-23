package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The vectors are shared with rag-server (tests/test_document_key.py): both
// sides must build the same per-page key or label rows and marks about one page
// stop lining up.
func TestDocumentKeyGoldenVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "docs", "fixtures", "kb_document_keys.json"))
	if err != nil {
		t.Fatalf("read golden vectors: %v", err)
	}
	var fixture struct {
		Cases []struct {
			Name       string         `json:"name"`
			Collection string         `json:"collection"`
			Metadata   map[string]any `json:"metadata"`
			URL        *string        `json:"url"`
			Key        string         `json:"key"`
			LabelKey   string         `json:"label_key"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("parse golden vectors: %v", err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("no golden vectors")
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			if got := DocumentKey(c.Collection, documentSourceID(c.Metadata), c.URL); got != c.Key {
				t.Errorf("DocumentKey = %q, want %q", got, c.Key)
			}
			if got := LabelKey(c.Collection, documentSourceID(c.Metadata), c.URL); got != c.LabelKey {
				t.Errorf("LabelKey = %q, want %q", got, c.LabelKey)
			}
		})
	}
}
