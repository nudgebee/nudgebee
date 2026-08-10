package vmpackage

import (
	"fmt"
	"strings"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/vulnmatcher"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

const (
	recommendationCategory = "Security"
	recommendationRuleName = "vm_package_vulnerability"

	// findingRowColumns is the column count in the recommendation upsert
	// below; findingBatchSize keeps each NamedExec's bind-parameter count
	// (rows*columns) under Postgres's 65535 limit. A single real host's
	// finding set can run into the thousands, so this isn't just a
	// defensive margin — a busy host hits the unbatched limit outright.
	findingRowColumns = 15
	findingBatchSize  = 65535 / findingRowColumns

	// packageRowColumns/packageBatchSize apply the same bind-parameter cap
	// to the vm_package upsert — a host's installed package count can run
	// into the thousands too.
	packageRowColumns = 15
	packageBatchSize  = 65535 / packageRowColumns
)

// packageRow is vm_package's insert shape as a struct rather than
// map[string]any: renaming/removing a field here breaks the build at every
// construction site, where a map key typo or a dropped map entry would
// instead compile clean and only surface as a runtime SQL error (extra/
// missing column) or a silently-NULL insert.
type packageRow struct {
	TenantID        string    `db:"tenant_id"`
	CloudAccountID  string    `db:"cloud_account_id"`
	CloudResourceID string    `db:"cloud_resource_id"`
	OSFamily        string    `db:"os_family"`
	OSVersion       string    `db:"os_version"`
	PkgType         string    `db:"pkg_type"`
	Name            string    `db:"name"`
	Version         string    `db:"version"`
	Arch            string    `db:"arch"`
	Epoch           *int      `db:"epoch"`
	SourceName      string    `db:"source_name"`
	SourceVersion   string    `db:"source_version"`
	IsActive        bool      `db:"is_active"`
	LastSeenAt      time.Time `db:"last_seen_at"`
	UpdatedAt       time.Time `db:"updated_at"`
}

// upsertPackages archives packages no longer reported for cloud_resource_id
// (a scan is a full snapshot: anything missing from this run is gone), then
// batch-upserts the current package list, all inside one transaction so an
// insert failure (bind-param overflow, constraint error) can never leave the
// VM looking package-free — the prior non-transactional archive-then-upsert
// (mirrored from cloud-collector's storeResourcesInsert / scan_orchestrator's
// persist.go) left exactly that window open. runScan bails out before
// calling this at all when pkgs is empty — a suspiciously empty parse
// result archiving the whole known inventory is the same silent-wipe risk
// this PR's suspect_zero handling guards against for findings.
func upsertPackages(tenantID, cloudAccountID, cloudResourceID, osFamily, osVersion string, pkgs []Package) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("vmpackage: db: %w", err)
	}

	now := time.Now()
	rows := make([]packageRow, 0, len(pkgs))
	for _, p := range pkgs {
		rows = append(rows, packageRow{
			TenantID:        tenantID,
			CloudAccountID:  cloudAccountID,
			CloudResourceID: cloudResourceID,
			OSFamily:        osFamily,
			OSVersion:       osVersion,
			PkgType:         p.Type,
			Name:            p.Name,
			Version:         p.Version,
			Arch:            p.Arch,
			Epoch:           p.Epoch,
			SourceName:      p.SourceName,
			SourceVersion:   p.SourceVersion,
			IsActive:        true,
			LastSeenAt:      now,
			UpdatedAt:       now,
		})
	}

	_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		if _, err := tx.Exec(
			`UPDATE vm_package SET is_active = false, updated_at = NOW() WHERE cloud_resource_id = $1 AND is_active`, cloudResourceID,
		); err != nil {
			return nil, fmt.Errorf("vmpackage: archive stale packages: %w", err)
		}

		for start := 0; start < len(rows); start += packageBatchSize {
			end := min(start+packageBatchSize, len(rows))
			if _, err := tx.NamedExec(
				`INSERT INTO vm_package
				   (tenant_id, cloud_account_id, cloud_resource_id, os_family, os_version, pkg_type, name, version,
				    arch, epoch, source_name, source_version, is_active, last_seen_at, updated_at)
				 VALUES
				   (:tenant_id, :cloud_account_id, :cloud_resource_id, :os_family, :os_version, :pkg_type, :name, :version,
				    :arch, :epoch, :source_name, :source_version, :is_active, :last_seen_at, :updated_at)
				 ON CONFLICT (cloud_resource_id, pkg_type, name, version, arch, epoch_key, source_name)
				 DO UPDATE SET is_active = true, last_seen_at = EXCLUDED.last_seen_at, updated_at = EXCLUDED.updated_at,
				               os_family = EXCLUDED.os_family, os_version = EXCLUDED.os_version`,
				rows[start:end],
			); err != nil {
				return nil, fmt.Errorf("vmpackage: upsert packages: %w", wrapPQError(err))
			}
		}
		return nil, nil
	})
	return err
}

// persistFindings archives this VM's previously open vm_package_vulnerability
// rows, then upserts the current findings, all inside one transaction so an
// insert failure can't leave every prior finding archived with nothing to
// replace them. Scoped by resource_id so scanning one VM never archives
// another VM's rows (same reasoning as scan_orchestrator/persist.go's
// per-image archive scope for image_scanner).
func persistFindings(tenantID, cloudAccountID, cloudResourceID string, findings []vulnmatcher.Finding, pkgsByKey map[string]Package) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("vmpackage: db: %w", err)
	}

	rows := buildFindingRows(tenantID, cloudAccountID, cloudResourceID, findings, pkgsByKey, time.Now())

	_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		if _, err := tx.Exec(
			`UPDATE recommendation SET status = 'Archive', updated_at = $1
			 WHERE tenant_id = $2 AND cloud_account_id = $3 AND category = $4 AND rule_name = $5
			   AND resource_id = $6 AND status != 'Archive'`,
			time.Now(), tenantID, cloudAccountID, recommendationCategory, recommendationRuleName, cloudResourceID,
		); err != nil {
			return nil, fmt.Errorf("vmpackage: archive existing findings: %w", err)
		}

		for start := 0; start < len(rows); start += findingBatchSize {
			end := min(start+findingBatchSize, len(rows))
			if _, err := tx.NamedExec(
				`INSERT INTO recommendation
				   (status, tenant_id, cloud_account_id, recommendation, severity, category, rule_name,
				    estimated_savings, recommendation_action, resource_id, account_object_id, updated_at,
				    finops_score, finops_band, finops_score_breakdown)
				 VALUES
				   (:status, :tenant_id, :cloud_account_id, :recommendation, :severity, :category, :rule_name,
				    :estimated_savings, :recommendation_action, :resource_id, :account_object_id, :updated_at,
				    :finops_score, :finops_band, :finops_score_breakdown)
				 ON CONFLICT (rule_name, cloud_account_id, resource_id, category, account_object_id)
				 DO UPDATE SET recommendation = EXCLUDED.recommendation,
				               status = EXCLUDED.status,
				               updated_at = EXCLUDED.updated_at,
				               severity = EXCLUDED.severity,
				               recommendation_action = EXCLUDED.recommendation_action`,
				rows[start:end],
			); err != nil {
				return nil, fmt.Errorf("vmpackage: upsert findings: %w", wrapPQError(err))
			}
		}
		return nil, nil
	})
	return err
}

// buildFindingRows turns findings into recommendation table rows, reusing
// the shared models.Recommendation struct (rather than a map[string]any or
// a bespoke local struct) so this insert stays in sync with every other
// rule writer's understanding of that table's columns. Resolves each
// Finding back to the Package that produced it (dropping any Finding for a
// Key we never sent — shouldn't happen, but defensive), and dedupes by the
// conflict-key's account_object_id component, since a NamedExec batch
// upsert fails outright if the same conflict key appears twice in one call.
func buildFindingRows(tenantID, cloudAccountID, cloudResourceID string, findings []vulnmatcher.Finding, pkgsByKey map[string]Package, now time.Time) []models.Recommendation {
	seen := make(map[string]struct{}, len(findings))
	rows := make([]models.Recommendation, 0, len(findings))
	for _, f := range findings {
		pkg, ok := pkgsByKey[f.Key]
		if !ok {
			continue
		}

		objectID := formatVMPackageObjectID(pkg, f.VulnID)
		if _, dup := seen[objectID]; dup {
			continue
		}
		seen[objectID] = struct{}{}

		severity := mapSeverity(f.Severity)
		recommendationAction := "Modify"
		finopsScore := 0
		finopsBand := ""

		rows = append(rows, models.Recommendation{
			Status:               models.RecommendationStatusOpen,
			TenantId:             tenantID,
			CloudAccountId:       cloudAccountID,
			Recommendation:       models.NewJsonObject(newFindingPayload(pkg, f)),
			Severity:             &severity,
			Category:             recommendationCategory,
			RuleName:             recommendationRuleName,
			EstimatedSavings:     0,
			RecommendationAction: &recommendationAction,
			ResourceId:           &cloudResourceID,
			AccountObjectId:      &objectID,
			UpdatedAt:            &now,
			FinOpsScore:          &finopsScore,
			FinOpsBand:           &finopsBand,
			FinOpsScoreBreakdown: models.NewJsonObject(map[string]any{}),
		})
	}
	return rows
}

func formatVMPackageObjectID(pkg Package, vulnID string) string {
	return fmt.Sprintf("%s-%s-%s-%s", pkg.Name, pkg.Version, pkg.Arch, vulnID)
}

// findingPackagePayload is the "package" sub-object inside findingPayload's
// JSON, built from the local Package type rather than vulnmatcher.Finding.
type findingPackagePayload struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Arch    string `json:"arch"`
	Type    string `json:"type"`
}

// findingPayload is the recommendation table's `recommendation` JSON blob
// for one vm_package_vulnerability finding — a struct rather than
// map[string]any so the shape is declared once and typed, instead of a set
// of string keys that Marshal/Unmarshal can silently drop or mistype.
type findingPayload struct {
	VulnID       string                `json:"vuln_id"`
	Package      findingPackagePayload `json:"package"`
	FixedVersion string                `json:"fixed_version"`
	FixState     string                `json:"fix_state"`
	FixChannel   string                `json:"fix_channel"`
	CVSSv3Score  float64               `json:"cvss_v3_score"`
	CVSSv3Vector string                `json:"cvss_v3_vector"`
	EPSS         *vulnmatcher.EPSS     `json:"epss"`
	KEV          bool                  `json:"kev"`
	Risk         float64               `json:"risk"`
	AdvisoryIDs  []string              `json:"advisory_ids"`
	DataSource   string                `json:"data_source"`
	Description  string                `json:"description"`
}

func newFindingPayload(pkg Package, f vulnmatcher.Finding) findingPayload {
	return findingPayload{
		VulnID: f.VulnID,
		Package: findingPackagePayload{
			Name:    pkg.Name,
			Version: pkg.Version,
			Arch:    pkg.Arch,
			Type:    pkg.Type,
		},
		FixedVersion: f.FixedVersion,
		FixState:     f.FixState,
		FixChannel:   f.FixChannel,
		CVSSv3Score:  f.CVSSv3Score,
		CVSSv3Vector: f.CVSSv3Vector,
		EPSS:         f.EPSS,
		KEV:          f.KEV,
		Risk:         f.Risk,
		AdvisoryIDs:  f.AdvisoryIDs,
		DataSource:   f.DataSource,
		Description:  f.Description,
	}
}

// mapSeverity normalizes vuln-matcher-server's severity string to the
// Title-cased vocabulary the recommendation table's other rule writers use
// (see scan_orchestrator's trivySeverity) — the wire contract doesn't pin
// casing, so normalize defensively rather than trust it verbatim.
func mapSeverity(s string) string {
	switch strings.ToUpper(s) {
	case "CRITICAL":
		return "Critical"
	case "HIGH":
		return "High"
	case "MEDIUM":
		return "Medium"
	case "LOW":
		return "Low"
	}
	return "Info"
}

func wrapPQError(err error) error {
	if pgErr, ok := err.(*pq.Error); ok {
		return fmt.Errorf("%s (constraint=%s detail=%s): %w", pgErr.Message, pgErr.Constraint, pgErr.Detail, err)
	}
	return err
}
