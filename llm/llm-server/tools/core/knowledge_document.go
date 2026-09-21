package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// ResolveKnowledgeSelection rechecks the active account row even for cached handles.
// Bodies are not loaded by this query. A manual version is the DB content MD5;
// the workspace copy is additionally verified with SHA256 after streaming.
func ResolveKnowledgeSelection(ctx *security.RequestContext, accountID, name, id string) (KnowledgeCandidate, string, error) {
	db, err := knowledgeDatabase(common.Metastore)
	if err != nil {
		return KnowledgeCandidate{}, "", fmt.Errorf("knowledge database: %w", err)
	}
	var c KnowledgeCandidate
	var kbType, category string
	var integration *string
	err = db.Db.QueryRowContext(ctx.GetContext(), `SELECT id, name, COALESCE(kb_type,'manual'), integration_id, md5(COALESCE(data,'')), octet_length(COALESCE(data,'')), COALESCE(note_category,'') FROM llm_knowledgebases WHERE account_id=$1 AND status='active' AND enabled AND (($2 <> '' AND id::text=$2) OR ($2='' AND LOWER(BTRIM(name))=LOWER(BTRIM($3)))) ORDER BY id LIMIT 1`, accountID, id, name).Scan(&c.KBID, &c.Title, &kbType, &integration, &c.Version, &c.Bytes, &category)
	if err != nil {
		return c, "", fmt.Errorf("knowledge is unavailable: %w", err)
	}
	c.Purpose = KnowledgePurpose(kbType, category)
	c.ReferenceID = c.KBID
	c.Source = "manual"
	c.Collection = KnowledgebaseCollectionName(Knowledgebase{Id: c.KBID, KBType: kbType, IntegrationId: integration})
	return c, kbType, nil
}

func WriteManualKnowledge(ctx *security.RequestContext, accountID string, c KnowledgeCandidate, out io.Writer) error {
	if c.Bytes > MaxKnowledgeDocumentBytes {
		return fmt.Errorf("manual knowledge exceeds transfer limit")
	}
	db, err := knowledgeDatabase(common.Metastore)
	if err != nil {
		return fmt.Errorf("knowledge database: %w", err)
	}
	// Substring is character based. Each individual SQL result is bounded; the
	// version predicate prevents mixing edits between chunks.
	var written int64
	const chunkChars = 256 * 1024
	for offset := 1; ; offset += chunkChars {
		var chunk string
		err := db.Db.QueryRowContext(ctx.GetContext(), `SELECT substring(data FROM $3::integer FOR $4::integer) FROM llm_knowledgebases WHERE account_id=$1 AND id::text=$2 AND status='active' AND enabled AND COALESCE(kb_type,'manual')='manual' AND md5(data)=$5`, accountID, c.KBID, offset, chunkChars, c.Version).Scan(&chunk)
		if err != nil {
			return fmt.Errorf("knowledge changed or became unavailable: %w", err)
		}
		if chunk == "" {
			break
		}
		written += int64(len(chunk))
		if written > MaxKnowledgeDocumentBytes {
			return fmt.Errorf("knowledge exceeds transfer limit")
		}
		if _, err := io.WriteString(out, chunk); err != nil {
			return fmt.Errorf("write knowledge: %w", err)
		}
	}
	if written != c.Bytes {
		return fmt.Errorf("knowledge content size changed")
	}
	return nil
}

// OpenKnowledgeDocument always checks current collection visibility and version.
func OpenKnowledgeDocument(ctx context.Context, accountID string, c KnowledgeCandidate, validateOnly bool) (io.ReadCloser, error) {
	if c.Collection == "" || c.DocumentID == "" || len(c.Version) != 64 {
		return nil, fmt.Errorf("exact indexed document handle unavailable; search again after updating RAG server")
	}
	payload, err := json.Marshal(map[string]any{"account_id": accountID, "collection_name": c.Collection, "document_id": c.DocumentID, "content_sha256": c.Version, "validate_only": validateOnly})
	if err != nil {
		return nil, fmt.Errorf("encode knowledge request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, getRAGServerURL()+"knowledge/document", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("knowledge request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	addRAGAuth(req)
	resp, err := ragClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch indexed knowledge: %w", err)
	}
	expected := http.StatusOK
	if validateOnly {
		expected = http.StatusNoContent
	}
	if resp.StatusCode != expected || resp.Header.Get("X-Content-SHA256") != c.Version || resp.ContentLength > MaxKnowledgeDocumentBytes {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("indexed knowledge unavailable, changed, or exceeds limit (status %d); search again", resp.StatusCode)
	}
	if validateOnly {
		_ = resp.Body.Close()
		return io.NopCloser(strings.NewReader("")), nil
	}
	return resp.Body, nil
}
