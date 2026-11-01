package triage

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"nudgebee/services/internal/database/models"

	"github.com/jmoiron/sqlx"
)

// ErrMintSuperseded is returned by saveSignalVerdict when its compare-and-swap finds no
// minting row to promote — the class was released or already minted by another goroutine.
// It is a benign race outcome, not an error to surface.
var ErrMintSuperseded = errors.New("signal-class mint superseded")

// ClassKey derives the signal-class cache key for an event:
//
//	lower(aggregation_key) | finding_type | scope
//
// where scope is the subject's owner, or the subject itself when it is an ownerless Kubernetes
// Service or workload, or else its namespace (see classSubject.identity).
//
// This collapses per-instance fingerprint fragmentation (e.g. 137 fingerprints of the same
// DB-slow-query alert -> one class) while separating conflated generic buckets (e.g. a
// "prod-alert" webhook carrying both RabbitMQ and slow-query alerts -> distinct scopes).
// Empirically validated against live data before adoption.
func ClassKey(event *models.Event) string {
	return classKeyFromParts(deref(event.AggregationKey), deref(event.FindingType), classSubjectOf(event))
}

// classKeyFromParts builds the class key from raw string parts, so callers that only have a
// partial event projection (e.g. eventBasicInfo) can derive the same key as ClassKey.
func classKeyFromParts(aggregationKey, findingType string, subject classSubject) string {
	aggKey := strings.ToLower(strings.TrimSpace(aggregationKey))
	finding := strings.ToLower(strings.TrimSpace(findingType))
	_, _, scope := subject.identity()
	return aggKey + "|" + finding + "|" + scope
}

// classSubject is the part of an event that says what a signal class is about.
type classSubject struct {
	Source    string
	Owner     string
	OwnerKind string
	Type      string
	Name      string
	Namespace string
}

func classSubjectOf(event *models.Event) classSubject {
	return classSubject{
		Source:    deref(event.Source),
		Owner:     deref(event.SubjectOwner),
		OwnerKind: deref(event.SubjectOwnerKind),
		Type:      deref(event.SubjectType),
		Name:      deref(event.SubjectName),
		Namespace: deref(event.SubjectNamespace),
	}
}

// selfScopedKinds are the Kubernetes kinds that are alerted on directly, with no owner above
// them, and whose name is a stable identity. Pods, jobs and nodes are left out on purpose: their
// names churn, and one verdict per alert kind is what they want.
var selfScopedKinds = map[string]bool{"service": true, "deployment": true, "statefulset": true, "daemonset": true}

// identity returns what the classifier is told the subject is (kind and name) and the scope
// segment of the class key. Both come from this one function so the prompt can never describe a
// different subject from the one the verdict is cached under.
//
// An ownerless Service or workload is scoped to itself. It used to fall back to the namespace,
// which gave every Service in a namespace one shared verdict, minted from whichever fired first
// and described to the classifier as the namespace. The namespace stays in the scope because the
// same name in two namespaces is two different things.
//
// Cloud-provider alerts are excluded: an ECS or Cloud Run alert also calls its subject a
// service, and its namespace field holds the provider's service name, not a namespace.
func (s classSubject) identity() (kind, name, scope string) {
	namespace := strings.TrimSpace(s.Namespace)
	if owner := strings.TrimSpace(s.Owner); owner != "" {
		return s.OwnerKind, owner, owner
	}
	subjectName := strings.TrimSpace(s.Name)
	if subjectName != "" && selfScopedKinds[strings.ToLower(strings.TrimSpace(s.Type))] && !isCloudProviderSource(s.Source) {
		return s.Type, subjectName, namespace + "/" + subjectName
	}
	return s.OwnerKind, namespace, namespace
}

// cloudProviderSourcePrefixes are how the cloud collector names its event sources:
// <provider>_<feed>, e.g. AWS_EventBridge, GCP_PubSub, Azure_EventGrid. Matching the prefix
// rather than a list of feeds keeps a feed added later on the cloud side of this line.
var cloudProviderSourcePrefixes = []string{"AWS_", "GCP_", "Azure_"}

func isCloudProviderSource(source string) bool {
	for _, prefix := range cloudProviderSourcePrefixes {
		if strings.HasPrefix(source, prefix) {
			return true
		}
	}
	return false
}

// getSignalVerdict returns the cached, servable verdict for a (tenant, class). The second
// return is false when no servable verdict exists yet (miss) — caller falls back. A class being
// lazily re-minted (status='reminting') keeps serving its existing verdict until the new one is
// promoted, so a prompt bump never causes a fallback-scoring blip.
func getSignalVerdict(ctx context.Context, db *sqlx.DB, tenantID, classKey string) (*SignalVerdict, bool) {
	if tenantID == "" || classKey == "" {
		return nil, false
	}
	var v SignalVerdict
	err := db.GetContext(ctx, &v, `
		SELECT category, intrinsic, blast, recurrence_semantics, env_sensitivity,
		       band_floor, band_ceiling, confidence,
		       COALESCE(reasoning, '')      AS reasoning,
		       COALESCE(source_model,'')    AS source_model,
		       COALESCE(prompt_version, 0)  AS prompt_version
		FROM triage_signal_class
		WHERE tenant_id = $1 AND class_key = $2 AND status IN ('active', 'reminting')
		LIMIT 1`, tenantID, classKey)
	if err != nil {
		return nil, false
	}
	return &v, true
}

// claimSignalClass atomically claims a class for minting across replicas. Returns true only
// for the caller that inserted the placeholder row; concurrent callers get false (they fall
// back and let the winner mint). Idempotent via the (tenant_id, class_key) unique constraint.
func claimSignalClass(ctx context.Context, db *sqlx.DB, tenantID, classKey string) bool {
	if tenantID == "" || classKey == "" {
		return false
	}
	// The 'minting' row is a lease. Without a TTL, a mint whose process died mid-flight (pod
	// restart/crash) leaves the row stuck in 'minting' forever — getSignalVerdict then misses and
	// every event of the class serves the fallback ('unclassified') permanently, while DO NOTHING
	// blocks any retry. So steal a STALE 'minting' lease (older than the 2-min mint timeout, with
	// margin) in the same atomic UPSERT. A fresh 'minting' (< TTL) or an 'active'/'reminting' row
	// keeps the WHERE false → no steal → claim returns false (correct). Self-heals orphaned claims.
	// updated_at is set explicitly (not relying on the column default) since the lease TTL check
	// below depends on it being a real timestamp, never NULL.
	res, err := db.ExecContext(ctx, `
		INSERT INTO triage_signal_class (tenant_id, class_key, status, updated_at)
		VALUES ($1, $2, 'minting', now())
		ON CONFLICT (tenant_id, class_key) DO UPDATE
		    SET status = 'minting', updated_at = now()
		    WHERE triage_signal_class.status = 'minting'
		      AND triage_signal_class.updated_at < now() - interval '15 minutes'`, tenantID, classKey)
	if err != nil {
		slog.ErrorContext(ctx, "failed to claim signal class", "tenant_id", tenantID, "class_key", classKey, "error", err)
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// saveSignalVerdict promotes a class to active with the minted verdict and stamps the prompt
// version it was minted under. It is a compare-and-swap on status IN ('minting','reminting') — a
// first mint claims via 'minting', a lazy re-mint via 'reminting'; either way it only writes the
// row this mint actually claimed and has not yet promoted. If the row was released, reverted, or
// already promoted by a newer mint, the UPDATE matches 0 rows and we return ErrMintSuperseded
// instead of resurrecting a stale verdict over a fresher one.
func saveSignalVerdict(ctx context.Context, db *sqlx.DB, tenantID, classKey string, v *SignalVerdict) error {
	if tenantID == "" || classKey == "" {
		return ErrMintSuperseded
	}
	res, err := db.ExecContext(ctx, `
		UPDATE triage_signal_class
		SET category=$1, intrinsic=$2, blast=$3, recurrence_semantics=$4, env_sensitivity=$5,
		    band_floor=$6, band_ceiling=$7, confidence=$8, reasoning=$9, source_model=$10,
		    prompt_version=$13, status='active', updated_at=now()
		WHERE tenant_id=$11 AND class_key=$12 AND status IN ('minting', 'reminting')`,
		v.Category, v.Intrinsic, v.Blast, v.RecurrenceSemantics, v.EnvSensitivity,
		v.BandFloor, v.BandCeiling, v.Confidence, v.Reasoning, v.SourceModel,
		tenantID, classKey, v.PromptVersion)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrMintSuperseded
	}
	return nil
}

// claimRemint atomically flips an active, stale verdict to 'reminting' so exactly one serving
// caller re-mints it. The row keeps serving its existing verdict (getSignalVerdict accepts
// 'reminting') until the new verdict is promoted. Returns true only for the CAS winner. The
// version guard makes it idempotent and self-rate-limiting: once flipped, no other serve re-claims.
func claimRemint(ctx context.Context, db *sqlx.DB, tenantID, classKey string, currentVersion int) bool {
	if tenantID == "" || classKey == "" {
		return false
	}
	res, err := db.ExecContext(ctx, `
		UPDATE triage_signal_class
		SET status='reminting', updated_at=now()
		WHERE tenant_id=$1 AND class_key=$2 AND status='active'
		  AND COALESCE(prompt_version, 0) < $3`, tenantID, classKey, currentVersion)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// revertRemint returns a reminting row to active WITHOUT changing its verdict — used when a
// re-mint fails, so the existing (older-prompt) verdict keeps serving instead of being lost.
func revertRemint(ctx context.Context, db *sqlx.DB, tenantID, classKey string) {
	if _, err := db.ExecContext(ctx,
		`UPDATE triage_signal_class SET status='active', updated_at=now()
		 WHERE tenant_id=$1 AND class_key=$2 AND status='reminting'`, tenantID, classKey); err != nil {
		slog.WarnContext(ctx, "failed to revert signal-class re-mint", "class_key", classKey, "error", err)
	}
}
