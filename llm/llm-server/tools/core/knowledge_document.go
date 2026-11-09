package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
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
	var kbType, category, format string
	var integration *string
	scope, scopeArgs := KBAccountScope("", 1, 4, VisibleOrgKnowledgebaseIDs(ctx.GetContext(), accountID))
	err = db.Db.QueryRowContext(ctx.GetContext(), `SELECT id, name, COALESCE(kb_type,'manual'), integration_id, md5(COALESCE(data,'')), octet_length(COALESCE(data,'')), COALESCE(note_category,''), COALESCE(data_format,'') FROM llm_knowledgebases WHERE `+scope+` AND status='active' AND enabled AND (($2 <> '' AND id::text=$2) OR ($2='' AND LOWER(BTRIM(name))=LOWER(BTRIM($3)))) ORDER BY id LIMIT 1`, append([]any{accountID, id, name}, scopeArgs...)...).Scan(&c.KBID, &c.Title, &kbType, &integration, &c.Version, &c.Bytes, &category, &format)
	if err != nil {
		return c, "", fmt.Errorf("knowledge is unavailable: %w", err)
	}
	c.Purpose = KnowledgePurpose(kbType, category)
	c.ReferenceID = c.KBID
	c.Source = "manual"
	c.Collection = KnowledgebaseCollectionName(Knowledgebase{Id: c.KBID, KBType: kbType, IntegrationId: integration})
	if format == string(KBFormatPDF) {
		// A PDF's data column is base64 of the document, so it cannot be streamed
		// as the body. Report it as indexed-only, which callers resolve like an
		// integration: through its page and figure chunks.
		return c, KnowledgeKindIndexedOnly, nil
	}
	return c, kbType, nil
}

// KnowledgeKindIndexedOnly is the kind ResolveKnowledgeSelection reports for a
// knowledge base whose stored data is not a readable body (a PDF).
const KnowledgeKindIndexedOnly = "indexed_only"

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
	scope, scopeArgs := KBAccountScope("", 1, 6, VisibleOrgKnowledgebaseIDs(ctx.GetContext(), accountID))
	for offset := 1; ; offset += chunkChars {
		var chunk string
		err := db.Db.QueryRowContext(ctx.GetContext(), `SELECT substring(data FROM $3::integer FOR $4::integer) FROM llm_knowledgebases WHERE `+scope+` AND id::text=$2 AND status='active' AND enabled AND COALESCE(kb_type,'manual')='manual' AND COALESCE(data_format,'') <> 'pdf' AND md5(data)=$5`, append([]any{accountID, c.KBID, offset, chunkChars, c.Version}, scopeArgs...)...).Scan(&chunk)
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

// KnowledgeScopeDocument is the X-Content-Scope rag-server answers with when it
// rebuilt the whole page from the chunk a handle points at. Anything else (or
// no header, from an older rag-server) means the body is the chunk itself.
const KnowledgeScopeDocument = "document"

// KnowledgeDocumentStream is an opened indexed read. For a whole-page body the
// handle's chunk hash and size no longer describe it, so the hash and size the
// response declares are exposed for the caller to verify the download against.
type KnowledgeDocumentStream struct {
	io.ReadCloser
	Scope  string
	SHA256 string
	Bytes  int64
}

// OpenKnowledgeDocument always checks current collection visibility and version.
// A read (not a validation) asks for the whole page the chunk belongs to; the
// server answers with the chunk when it cannot rebuild it cleanly.
func OpenKnowledgeDocument(ctx context.Context, accountID string, c KnowledgeCandidate, validateOnly bool) (*KnowledgeDocumentStream, error) {
	if c.Collection == "" || c.DocumentID == "" || len(c.Version) != 64 {
		return nil, fmt.Errorf("exact indexed document handle unavailable; search again after updating RAG server")
	}
	payload, err := json.Marshal(map[string]any{"account_id": accountID, "collection_name": c.Collection, "document_id": c.DocumentID, "content_sha256": c.Version, "validate_only": validateOnly, "whole_document": !validateOnly})
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
	stream := &KnowledgeDocumentStream{ReadCloser: resp.Body, Scope: resp.Header.Get("X-Content-Scope"), SHA256: resp.Header.Get("X-Content-SHA256"), Bytes: c.Bytes}
	wantSHA := c.Version
	if stream.Scope == KnowledgeScopeDocument && !validateOnly {
		// The page has its own hash and size; the handle's pinned chunk hash was
		// already checked by the server before it assembled anything.
		wantSHA = stream.SHA256
		size, sizeErr := strconv.ParseInt(resp.Header.Get("X-Content-Bytes"), 10, 64)
		if len(wantSHA) != 64 || sizeErr != nil || size < 0 || size > MaxKnowledgeDocumentBytes {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("indexed knowledge unavailable: malformed or oversized whole-document response; search again")
		}
		stream.Bytes = size
	}
	if resp.StatusCode != expected || stream.SHA256 != wantSHA || resp.ContentLength > MaxKnowledgeDocumentBytes {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("indexed knowledge unavailable, changed, or exceeds limit (status %d); search again", resp.StatusCode)
	}
	if validateOnly {
		_ = resp.Body.Close()
		stream.ReadCloser = io.NopCloser(strings.NewReader(""))
	}
	return stream, nil
}
