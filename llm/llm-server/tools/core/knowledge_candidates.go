package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"nudgebee/llm/common"
)

const CacheNamespaceLlmKnowledgeCandidates = "llm_knowledge_candidates"

const MaxKnowledgeCandidateBytes = 32 * 1024
const KnowledgeExcerptBytes = 4096
const MaxKnowledgeDocumentBytes = 32 * 1024 * 1024

// KnowledgeCandidate retains a bounded excerpt and an exact document handle.
type KnowledgeCandidate struct {
	Purpose     KnowledgeContentPurpose `json:"purpose,omitempty"`
	ID          string                  `json:"id"`
	Collection  string                  `json:"collection,omitempty"`
	DocumentID  string                  `json:"document_id,omitempty"`
	Version     string                  `json:"version,omitempty"`
	Bytes       int64                   `json:"bytes,omitempty"`
	Path        string                  `json:"path,omitempty"`
	FileSHA256  string                  `json:"file_sha256,omitempty"`
	ExcerptOnly bool                    `json:"excerpt_only,omitempty"`
	KBID        string                  `json:"kb_id,omitempty"`
	ReferenceID string                  `json:"reference_id,omitempty"`
	Title       string                  `json:"title"`
	Source      string                  `json:"source"`
	URL         string                  `json:"url,omitempty"`
	Snippet     string                  `json:"snippet,omitempty"`
	Content     string                  `json:"content"`
}

func init() {
	common.CacheCreateNamespace(CacheNamespaceLlmKnowledgeCandidates,
		common.CacheNamespaceWithExpiration(30*time.Minute),
		common.CacheNamespaceWithMaxEntries(10000))
}

func NewKnowledgeCandidateID(accountID, conversationID, messageID, identity string) string {
	sum := sha256.Sum256([]byte(accountID + "\x00" + conversationID + "\x00" + messageID + "\x00" + identity))
	return "knowledge:" + hex.EncodeToString(sum[:8])
}

func knowledgeCandidateCacheKey(accountID, conversationID, messageID, id string) string {
	return fmt.Sprintf("candidate:%s:%s:%s:%s", accountID, conversationID, messageID, strings.ToLower(strings.TrimSpace(id)))
}

func StoreKnowledgeCandidate(accountID, conversationID, messageID string, candidate KnowledgeCandidate) error {
	if len(candidate.Content) > KnowledgeExcerptBytes {
		return fmt.Errorf("knowledge content exceeds excerpt limit")
	}
	raw, err := common.MarshalJson(candidate)
	if err != nil {
		return err
	}
	if len(raw) > MaxKnowledgeCandidateBytes {
		return fmt.Errorf("knowledge candidate exceeds %d bytes; narrow the retrieval query", MaxKnowledgeCandidateBytes)
	}
	return common.CacheSet(CacheNamespaceLlmKnowledgeCandidates,
		knowledgeCandidateCacheKey(accountID, conversationID, messageID, candidate.ID), raw, common.CacheSetWithExpiration(30*time.Minute))
}

func LoadKnowledgeCandidate(accountID, conversationID, messageID, id string) (KnowledgeCandidate, bool) {
	raw, ok := common.CacheGet(CacheNamespaceLlmKnowledgeCandidates,
		knowledgeCandidateCacheKey(accountID, conversationID, messageID, id))
	if !ok {
		return KnowledgeCandidate{}, false
	}
	var candidate KnowledgeCandidate
	if err := common.UnmarshalJson(raw, &candidate); err != nil {
		return KnowledgeCandidate{}, false
	}
	return candidate, true
}

// SetKnowledgeDocumentHandle strips large bodies even when an older RAG server
// ignores the excerpt request. Such results must be labelled excerpt-only.
func SetKnowledgeDocumentHandle(candidate *KnowledgeCandidate, doc RAGSearchResult) {
	candidate.Purpose = KnowledgeDocumentPurpose(doc)
	candidate.Collection, _ = doc.Metadata["collection"].(string)
	candidate.DocumentID, _ = doc.Metadata["retrieval_id"].(string)
	candidate.Version, _ = doc.Metadata["content_sha256"].(string)
	if size, ok := doc.Metadata["content_bytes"].(float64); ok {
		candidate.Bytes = int64(size)
	}
	candidate.ExcerptOnly = len(candidate.Content) > KnowledgeExcerptBytes || candidate.Bytes > int64(len(candidate.Content))
	candidate.Content = common.TruncateHead(candidate.Content, KnowledgeExcerptBytes)
}

func KnowledgeDocumentIdentity(doc RAGSearchResult) string {
	if id := ManualKnowledgeID(doc); id != "" {
		return "manual:" + id
	}
	collection, _ := doc.Metadata["collection"].(string)
	id, _ := doc.Metadata["retrieval_id"].(string)
	version, _ := doc.Metadata["content_sha256"].(string)
	if collection != "" && id != "" {
		return collection + "\x00" + id + "\x00" + version
	}
	return ""
}

// ManualKnowledgeID accepts the canonical manual collection identity, never a
// similarly named metadata field attached to an external document.
func ManualKnowledgeID(doc RAGSearchResult) string {
	collection, _ := doc.Metadata["collection"].(string)
	id, _ := doc.Metadata["kb_id"].(string)
	if id != "" && collection == "kb_"+id {
		return id
	}
	return ""
}
