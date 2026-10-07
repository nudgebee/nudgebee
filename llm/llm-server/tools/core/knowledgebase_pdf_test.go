package core

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// What survives here tests the two helpers llm-server still owns for PDFs: the
// recorded size of an uploaded document, and the stored-payload cap. Extraction,
// figure description and chunk assembly moved to rag-server and are tested there
// (llm/rag-server/tests/test_pdf_*.py).

func TestDecodeKBPDFSizeReturnsDecodedLength(t *testing.T) {
	raw := append([]byte("%PDF-1.7\n"), make([]byte, 100)...)
	encoded := base64.StdEncoding.EncodeToString(raw)

	size, err := DecodeKBPDFSize(encoded)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if size != int64(len(raw)) {
		t.Fatalf("expected the decoded length %d, got %d", len(raw), size)
	}
	if size >= int64(len(encoded)) {
		t.Fatal("size should be the document's, not the base64 inflation's")
	}
}

func TestDecodeKBPDFSizeRejectsNonPDF(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("just some text"))

	if _, err := DecodeKBPDFSize(encoded); err == nil {
		t.Fatal("expected a non-PDF payload to be rejected")
	}
}

func TestDecodeKBPDFSizeRejectsBrokenBase64(t *testing.T) {
	if _, err := DecodeKBPDFSize("not base64 !!!"); err == nil {
		t.Fatal("expected invalid base64 to be rejected")
	}
}

func TestResolveKBDataSizeUsesRawLengthForText(t *testing.T) {
	size, err := resolveKBDataSize("hello", string(KBFormatText))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if size != 5 {
		t.Fatalf("expected 5, got %d", size)
	}
}

func TestResolveKBDataSizeRejectsOversizedPayload(t *testing.T) {
	if _, err := resolveKBDataSize(strings.Repeat("x", maxKBSizeBytes+1), string(KBFormatText)); err == nil {
		t.Fatal("expected the stored-payload cap to be enforced")
	}
}

func TestDecodeKBPDFSizeAcceptsHeaderPrecededByJunk(t *testing.T) {
	// Readers tolerate leading bytes before %PDF-, and rag-server's extractor opens
	// such a file, so rejecting it here would refuse a document the pipeline can read.
	raw := append([]byte("\r\n\x00junk-prefix\r\n"), []byte("%PDF-1.7\n")...)
	raw = append(raw, make([]byte, 100)...)
	encoded := base64.StdEncoding.EncodeToString(raw)

	size, err := DecodeKBPDFSize(encoded)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if size != int64(len(raw)) {
		t.Fatalf("expected the decoded length %d, got %d", len(raw), size)
	}
}

func TestDecodeKBPDFSizeRejectsHeaderBeyondSearchWindow(t *testing.T) {
	raw := append([]byte(strings.Repeat("z", 2000)), []byte("%PDF-1.7\n")...)

	if _, err := DecodeKBPDFSize(base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("expected a header outside the search window to be rejected")
	}
}

func TestDecodeKBPDFSizeToleratesWhitespace(t *testing.T) {
	// rag-server's decode_pdf discards every non-alphabet byte, so the two gates must
	// agree: Go skips \r and \n on its own but rejects spaces and tabs.
	raw := append([]byte("%PDF-1.7\n"), make([]byte, 100)...)
	clean := base64.StdEncoding.EncodeToString(raw)

	for name, encoded := range map[string]string{
		"newline": clean[:20] + "\n" + clean[20:],
		"space":   clean[:20] + " " + clean[20:],
		"tab":     clean[:20] + "\t" + clean[20:],
		"wrapped": clean[:20] + "\r\n" + clean[20:],
	} {
		size, err := DecodeKBPDFSize(encoded)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if size != int64(len(raw)) {
			t.Fatalf("%s: expected %d, got %d", name, len(raw), size)
		}
	}
}

func TestResolveKBIngestPayloadScopesFilenameByAccount(t *testing.T) {
	// The filename lookup must not cross tenants: knowing a uuid should not be
	// enough to read another account's document name.
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	oldDatabase := knowledgeDatabase
	knowledgeDatabase = func(common.DatabaseManagerType) (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}, nil
	}
	t.Cleanup(func() { knowledgeDatabase = oldDatabase })

	mock.ExpectQuery(`SELECT COALESCE\(data_filename, ''\) FROM llm_knowledgebases WHERE id = \$1 AND account_id = \$2`).
		WithArgs("kb-1", "account-a").
		WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow("runbook.pdf"))

	_, _, filename, err := resolveKBIngestPayload(
		security.NewRequestContextForSuperAdmin(), kbOwner{AccountId: "account-a"}, "kb-1", "ZGF0YQ==", string(KBFormatPDF))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filename != "runbook.pdf" {
		t.Fatalf("expected runbook.pdf, got %q", filename)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("query did not carry the account scope: %v", err)
	}
}
