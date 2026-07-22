package templatesource

import (
	"context"
	"log/slog"

	"nudgebee/runbook/internal/storage"
)

// Syncer reconciles the configured Source into workflow_templates. Invocation is
// driven by a Temporal Schedule (see schedule.go) — single cluster-wide execution,
// so no cross-replica lock is needed; correctness also rests on the idempotent
// upsert. SyncOnce is reused by the admin force-sync RPC.
type Syncer struct {
	dao               *storage.WorkflowTemplateDao
	source            Source
	logger            *slog.Logger
	deactivateMissing bool
}

func NewSyncer(dao *storage.WorkflowTemplateDao, source Source, logger *slog.Logger, deactivateMissing bool) *Syncer {
	return &Syncer{dao: dao, source: source, logger: logger, deactivateMissing: deactivateMissing}
}

// SyncOnce performs a single reconcile pass: fetch the bundle, then upsert + drift
// reconcile in one transaction. A bad/empty fetch is skipped (never reconciled).
func (s *Syncer) SyncOnce(ctx context.Context) (storage.ReconcileResult, error) {
	var empty storage.ReconcileResult

	bundle, err := s.source.Fetch(ctx)
	if err != nil {
		return empty, err
	}
	if bundle == nil || !bundle.OK || len(bundle.Templates) == 0 {
		// Never reconcile (and never deactivate) on a bad/empty fetch.
		s.logger.Warn("template sync: empty or failed fetch, skipping reconcile", "source", s.source.Name())
		return empty, nil
	}

	rows := make([]storage.SystemTemplateInput, len(bundle.Templates))
	for i, t := range bundle.Templates {
		rows[i] = t.toInput()
	}

	res, err := s.dao.ReconcileSystemTemplates(ctx, s.source.Name(), bundle.Ref, rows, s.deactivateMissing)
	if err != nil {
		return empty, err
	}
	s.logger.Info("template sync complete",
		"source", s.source.Name(), "ref", bundle.Ref,
		"fetched", len(bundle.Templates), "upserted", res.Upserted, "deactivated", res.Deactivated)
	return res, nil
}
