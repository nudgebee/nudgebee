// Package backfill migrates existing llm_conversation_memory rows into the
// typed Memory Module stores. Run as a one-shot binary or invoked from an
// admin endpoint. Idempotent: uses per-row idempotency keys so re-runs are
// safe.
package backfill

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/memory"
	"time"
)

// Progress reports the result of a backfill pass.
type Progress struct {
	Scanned       int
	Classified    int
	Quarantined   int
	Errors        int
	Duration      time.Duration
	ByTargetStore map[memory.TargetStore]int
}

// legacyRow is the shape we read from llm_conversation_memory.
// Kept here (not imported from agents/core) because the DAO over there is
// tightly coupled to the runtime types; backfill only needs the raw fields.
type legacyRow struct {
	ID             string  `db:"id"`
	TenantID       string  `db:"tenant_id"`
	AccountID      string  `db:"account_id"`
	UserID         *string `db:"user_id"`
	ConversationID *string `db:"conversation_id"`
	MemoryType     string  `db:"memory_type"`
	Content        string  `db:"content"`
	CreatedAt      time.Time `db:"created_at"`
}

// Options controls a single backfill pass.
type Options struct {
	TenantID  string    // if set, restricts to one tenant
	Since     time.Time // zero = all time
	BatchSize int       // rows per page
	MaxBatch  int       // cap total rows this run (0 = unlimited)
	DryRun    bool      // when true, classifies but does not write
}

// Run scans llm_conversation_memory and projects rows into typed stores.
func Run(ctx context.Context, opts Options) (*Progress, error) {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 500
	}
	progress := &Progress{ByTargetStore: map[memory.TargetStore]int{}}
	start := time.Now()
	defer func() { progress.Duration = time.Since(start) }()

	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return progress, fmt.Errorf("backfill: db: %w", err)
	}

	offset := 0
	for {
		var rows []legacyRow
		query, args := buildLegacyScanQuery(opts, offset)
		if err := db.Db.Select(&rows, query, args...); err != nil {
			return progress, fmt.Errorf("backfill: scan: %w", err)
		}
		if len(rows) == 0 {
			break
		}

		for _, r := range rows {
			progress.Scanned++
			if err := projectLegacyRow(ctx, r, opts.DryRun); err != nil {
				progress.Errors++
				slog.Warn("backfill: project failed", "id", r.ID, "error", err)
				continue
			}
			target := memory.ClassifyLegacyType(r.MemoryType)
			progress.ByTargetStore[target]++
			if target == memory.TargetQuarantine {
				progress.Quarantined++
			} else {
				progress.Classified++
			}
		}

		offset += len(rows)
		if opts.MaxBatch > 0 && progress.Scanned >= opts.MaxBatch {
			break
		}
		if len(rows) < opts.BatchSize {
			break
		}
	}

	slog.Info("backfill: pass complete",
		"scanned", progress.Scanned,
		"classified", progress.Classified,
		"quarantined", progress.Quarantined,
		"errors", progress.Errors,
		"duration_ms", progress.Duration.Milliseconds())
	return progress, nil
}

// projectLegacyRow translates one legacy row into a LegacyMemoryFact and
// hands it to the bridge. DryRun skips the write; classification still runs
// so quarantine counters are meaningful.
func projectLegacyRow(_ context.Context, r legacyRow, dryRun bool) error {
	if dryRun {
		return nil
	}
	userID := ""
	if r.UserID != nil {
		userID = *r.UserID
	}
	convID := ""
	if r.ConversationID != nil {
		convID = *r.ConversationID
	}
	return memory.BridgeFromLegacy(memory.LegacyMemoryFact{
		TenantID:       r.TenantID,
		UserID:         userID,
		ConversationID: convID,
		MemoryType:     r.MemoryType,
		Subject:        compactSubject(r.Content),
		Content:        r.Content,
		Metadata:       map[string]any{"legacy_id": r.ID, "account_id": r.AccountID},
		IdempotencyKey: r.ID,
	})
}

// compactSubject derives a short subject from raw content for display.
// Uses the first line or first 80 chars.
func compactSubject(content string) string {
	for i, c := range content {
		if c == '\n' {
			return content[:i]
		}
		if i >= 80 {
			return content[:i]
		}
	}
	return content
}

func buildLegacyScanQuery(opts Options, offset int) (string, []any) {
	args := []any{}
	where := "1=1"
	if opts.TenantID != "" {
		args = append(args, opts.TenantID)
		where += fmt.Sprintf(" AND tenant_id = $%d", len(args))
	}
	if !opts.Since.IsZero() {
		args = append(args, opts.Since)
		where += fmt.Sprintf(" AND created_at >= $%d", len(args))
	}
	args = append(args, opts.BatchSize, offset)
	query := fmt.Sprintf(`
		SELECT id, tenant_id, account_id, user_id, conversation_id,
		       memory_type, content, created_at
		FROM llm_conversation_memory
		WHERE %s
		ORDER BY created_at ASC
		LIMIT $%d OFFSET $%d
	`, where, len(args)-1, len(args))
	return query, args
}
