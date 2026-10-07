package core

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"

	"nudgebee/llm/audit"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// What llm-server still owns for a PDF knowledge base: the recorded size of the
// uploaded document, the stored-payload cap, and handing the document to
// rag-server to be read.
//
// Reading the PDF, describing its figures with a vision model and assembling the
// indexed chunks all happen in rag-server, where the sources and the indexer
// already live (llm/rag-server/rag/core/documents/pdf_ingest.py).

// PDFKnowledgePointer stands in for a PDF knowledge base's body wherever a
// reader would otherwise serve the data column, which holds base64 of the
// document. The indexed page and figure chunks are the readable content.
const PDFKnowledgePointer = "This knowledge base is a PDF document. Its pages and figures are indexed " +
	"separately: use search_skills with a specific question to retrieve the relevant passages."

const kbPDFHeaderSearchBytes = 1024

var kbPDFExtractClient = &http.Client{
	// ResponseHeaderTimeout bounds the wait for headers only; without an overall
	// budget a stalled body read holds an embedding worker forever.
	Timeout: 600 * time.Second,
	Transport: &http.Transport{
		ResponseHeaderTimeout: 300 * time.Second,
		IdleConnTimeout:       10 * time.Second,
		MaxIdleConnsPerHost:   2,
	},
}

func DecodeKBPDFSize(base64Data string) (int64, error) {
	// Go's decoder skips \r and \n but rejects spaces and tabs, where rag-server's
	// decode_pdf discards every non-alphabet byte. Without this the two gates disagree
	// and a payload rag-server would read is refused at upload.
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, base64Data)

	decoded, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil {
		return 0, errors.New("the uploaded file could not be read; please re-select it and try again")
	}
	// Searched within the first 1024 bytes rather than required at offset 0: readers
	// tolerate a header preceded by junk, and rag-server's extractor opens such a file,
	// so rejecting it here would refuse a document the rest of the pipeline can read.
	head := decoded
	if len(head) > kbPDFHeaderSearchBytes {
		head = head[:kbPDFHeaderSearchBytes]
	}
	if !bytes.Contains(head, []byte("%PDF-")) {
		return 0, errors.New("this file is not a valid PDF")
	}
	return int64(len(decoded)), nil
}

// resolveKBDataSize enforces the stored-payload cap and returns the size to
// record on the KB.
//
// The cap is checked against the stored payload because that is what occupies
// the column, but a PDF's recorded size is its decoded length — reporting the
// base64 inflation as the document's size would be wrong on every card.
func resolveKBDataSize(data, format string) (int64, error) {
	if int64(len(data)) > maxKBSizeBytes {
		return 0, fmt.Errorf("%s: %d bytes", errKBSizeTooLarge, len(data))
	}
	if format == string(KBFormatPDF) {
		return DecodeKBPDFSize(data)
	}
	return int64(len(data)), nil
}

func resolveKBIngestPayload(
	sc *security.RequestContext,
	accountId, kbId, data, format string,
) (ragData, ragFormat, filename string, err error) {
	if format != string(KBFormatPDF) {
		return data, format, "", nil
	}

	dbms, dbErr := knowledgeDatabase(common.Metastore)
	if dbErr != nil {
		return "", "", "", fmt.Errorf("kb: failed to get database manager: %w", dbErr)
	}
	// Scoped by account as well as id: every other read of this table is, and an
	// unscoped lookup would return another tenant's filename to whoever knows a uuid.
	scope, scopeArgs := KBAccountScope("", 2, 3, VisibleOrgKnowledgebaseIDs(sc.GetContext(), accountId))
	if nameErr := dbms.Db.Get(&filename,
		`SELECT COALESCE(data_filename, '') FROM llm_knowledgebases WHERE id = $1 AND `+scope,
		append([]any{kbId, accountId}, scopeArgs...)...); nameErr != nil {
		// Only affects the title on each chunk, so it is not worth failing over.
		slog.Warn("kb: failed to read PDF filename", "error", nameErr, "kb_id", kbId)
	}
	return data, format, filename, nil
}

func recordKBIngestFailure(
	sc *security.RequestContext,
	dbms *common.DatabaseManager,
	accountId, kbId string,
	eventType audit.EventType,
	cause error,
) {
	_ = updateKBStatusError(dbms, kbId, cause.Error())

	auditReq := &audit.AuditRequest{
		Audits: []audit.Audit{
			{
				AccountId:     accountId,
				EventTime:     time.Now().UTC(),
				EventCategory: "knowledgebase",
				EventType:     eventType,
				EventActor:    audit.EventActor(sc.GetSecurityContext().GetUserId()),
				EventTarget:   kbId,
				EventAction:   audit.EventActionUpdate,
				EventStatus:   audit.EventStatusFailure,
				TransactionId: sc.GetTraceId(),
				EventAttr:     map[string]any{"kb_id": kbId, "error": cause.Error()},
				TenantId:      sc.GetSecurityContext().GetTenantId(),
				UserId:        sc.GetSecurityContext().GetUserId(),
			},
		},
	}
	if auditErr := audit.CreateAudit(sc, auditReq); auditErr != nil {
		slog.Error("kb: failed to create audit entry", "error", auditErr, "kb_id", kbId)
	}
}
