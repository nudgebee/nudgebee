package vmpackage

import (
	"fmt"
	"slices"
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
	findingRowColumns = 16
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

// source_version is in the DO UPDATE list above even though source_name is part
// of the conflict key: an existing row keeps whatever source_version it was
// first written with, so without it every row stored before #36278 would keep
// its empty value forever — a rescan would refresh last_seen_at and still leave
// the column blank. Verified against dev: a full rescan updated last_seen_at
// while source_version stayed "" until this was added.
//
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
				               os_family = EXCLUDED.os_family, os_version = EXCLUDED.os_version,
				               source_version = EXCLUDED.source_version`,
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

	rows, vulnRows, vulnKeys := buildFindingRows(tenantID, cloudAccountID, cloudResourceID, findings, pkgsByKey, time.Now())

	_, err = dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {
		if _, err := tx.Exec(
			`UPDATE recommendation SET status = 'Archive', updated_at = $1
			 WHERE tenant_id = $2 AND cloud_account_id = $3 AND category = $4 AND rule_name = $5
			   AND resource_id = $6 AND status != 'Archive'`,
			time.Now(), tenantID, cloudAccountID, recommendationCategory, recommendationRuleName, cloudResourceID,
		); err != nil {
			return nil, fmt.Errorf("vmpackage: archive existing findings: %w", err)
		}

		vulnIDs, err := models.UpsertVulnerabilities(tx, vulnRows)
		if err != nil {
			return nil, fmt.Errorf("vmpackage: upsert vulnerabilities: %w", wrapPQError(err))
		}
		for i := range rows {
			if id, ok := vulnIDs[vulnKeys[i]]; ok {
				rows[i].VulnerabilityId = &id
			}
		}

		for start := 0; start < len(rows); start += findingBatchSize {
			end := min(start+findingBatchSize, len(rows))
			if _, err := tx.NamedExec(
				`INSERT INTO recommendation
				   (status, tenant_id, cloud_account_id, recommendation, severity, category, rule_name,
				    estimated_savings, recommendation_action, resource_id, account_object_id, updated_at,
				    finops_score, finops_band, finops_score_breakdown, vulnerability_id)
				 VALUES
				   (:status, :tenant_id, :cloud_account_id, :recommendation, :severity, :category, :rule_name,
				    :estimated_savings, :recommendation_action, :resource_id, :account_object_id, :updated_at,
				    :finops_score, :finops_band, :finops_score_breakdown, :vulnerability_id)
				 ON CONFLICT (rule_name, cloud_account_id, resource_id, category, account_object_id)
				 DO UPDATE SET recommendation = EXCLUDED.recommendation,
				               status = EXCLUDED.status,
				               updated_at = EXCLUDED.updated_at,
				               severity = EXCLUDED.severity,
				               recommendation_action = EXCLUDED.recommendation_action,
				               vulnerability_id = EXCLUDED.vulnerability_id`,
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
//
// Alongside each recommendation row it builds the matching
// models.Vulnerability row and its VulnerabilityKey, index-aligned with
// rows, so the caller can upsert vulnerabilities first and then populate
// each row's VulnerabilityId from the returned id map.
func buildFindingRows(tenantID, cloudAccountID, cloudResourceID string, findings []vulnmatcher.Finding, pkgsByKey map[string]Package, now time.Time) ([]models.Recommendation, []models.Vulnerability, []string) {
	type groupedFinding struct {
		finding         vulnmatcher.Finding
		vulnerablePkg   Package
		vulnerableNames []string
	}
	groups := make(map[string]*groupedFinding, len(findings))
	groupOrder := make([]string, 0, len(findings))

	// Grype reports a match for every installed binary package. Advisories are
	// normally published for the source/vendor package that built those
	// binaries, so group at that level while retaining every binary name.
	for _, f := range findings {
		pkg, ok := pkgsByKey[f.Key]
		if !ok {
			continue
		}

		objectID := formatVMPackageObjectID(pkg, f.VulnID)
		group, exists := groups[objectID]
		if !exists {
			groups[objectID] = &groupedFinding{
				finding:         f,
				vulnerablePkg:   canonicalVulnerablePackage(pkg),
				vulnerableNames: []string{pkg.Name},
			}
			groupOrder = append(groupOrder, objectID)
			continue
		}
		if !slices.Contains(group.vulnerableNames, pkg.Name) {
			group.vulnerableNames = append(group.vulnerableNames, pkg.Name)
			slices.Sort(group.vulnerableNames)
		}
	}

	rows := make([]models.Recommendation, 0, len(groups))
	vulnRows := make([]models.Vulnerability, 0, len(groups))
	vulnKeys := make([]string, 0, len(groups))
	for _, objectID := range groupOrder {
		group := groups[objectID]
		f := group.finding
		pkg := group.vulnerablePkg

		severity := mapSeverity(f.Severity)
		recommendationAction := "Modify"
		finopsScore := 0
		finopsBand := ""

		rows = append(rows, models.Recommendation{
			Status:         models.RecommendationStatusOpen,
			TenantId:       tenantID,
			CloudAccountId: cloudAccountID,
			// Only fixed_version/fix_state/package_version stay here — the
			// per-finding remediation context (package_version is the version
			// actually installed on THIS resource, not a property of the
			// vulnerability itself, so it never belonged on the vulnerabilities
			// row). Everything else this used to carry (vuln_id, package name,
			// cvss, epss/kev/risk, description, ...) now lives on the linked
			// vulnerabilities row via VulnerabilityId below; readers get the
			// full legacy shape back via the vulnerabilities join at query
			// time (see metadata.go's recommendations_v2/recommendation_groupings_v2).
			Recommendation: models.NewJsonObject(map[string]any{
				"fixed_version":       f.FixedVersion,
				"fix_state":           f.FixState,
				"package_version":     pkg.Version,
				"vulnerable_packages": group.vulnerableNames,
			}),
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

		vulnRows = append(vulnRows, buildVulnerabilityRow(pkg, f))
		vulnKeys = append(vulnKeys, models.VulnerabilityKey(models.VulnerabilitySourceVMPackage, f.VulnID, pkg.Name, nonEmptyPtr(pkg.Arch)))
	}
	return rows, vulnRows, vulnKeys
}

// buildVulnerabilityRow flattens a Finding+Package into the vulnerabilities
// table's shape, mirroring newFindingPayload's field selection. The fields
// not promoted to their own column (fix_state, fix_channel, epss, kev, risk,
// advisory_ids) are kept in Details, same set the migration's backfill puts
// there for historical rows.
func buildVulnerabilityRow(pkg Package, f vulnmatcher.Finding) models.Vulnerability {
	severity := mapSeverity(f.Severity)
	return models.Vulnerability{
		Source:       models.VulnerabilitySourceVMPackage,
		VulnId:       f.VulnID,
		PackageName:  pkg.Name,
		PackageArch:  nonEmptyPtr(pkg.Arch),
		PackageType:  nonEmptyPtr(pkg.Type),
		FixedVersion: nonEmptyPtr(f.FixedVersion),
		Severity:     &severity,
		CVSSScore:    &f.CVSSv3Score,
		CVSSVector:   nonEmptyPtr(f.CVSSv3Vector),
		Description:  nonEmptyPtr(f.Description),
		DataSource:   nonEmptyPtr(f.DataSource),
		Details: models.NewJsonObject(map[string]any{
			"fix_state":    f.FixState,
			"fix_channel":  f.FixChannel,
			"epss":         f.EPSS,
			"kev":          f.KEV,
			"risk":         f.Risk,
			"advisory_ids": f.AdvisoryIDs,
		}),
	}
}

// nonEmptyPtr returns nil for an empty string, otherwise a pointer to it —
// used for columns that should stay NULL rather than store "" (package_arch,
// fixed_version, etc.), matching the migration backfill's NULLIF handling of
// the same fields.
func nonEmptyPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func formatVMPackageObjectID(pkg Package, vulnID string) string {
	pkg = canonicalVulnerablePackage(pkg)
	return fmt.Sprintf("%s-%s-%s-%s", pkg.Name, pkg.Version, pkg.Arch, vulnID)
}

// canonicalVulnerablePackage returns the package the advisory is actually
// about. Binary packages such as kernel-core and kernel-modules share one
// source package and must therefore share one recommendation identity.
func canonicalVulnerablePackage(pkg Package) Package {
	if pkg.SourceName == "" || pkg.SourceVersion == "" {
		return pkg
	}

	pkg.Name = canonicalSourceName(pkg.SourceName)
	if pkg.SourceVersion != "" {
		pkg.Version = pkg.SourceVersion
	}
	return pkg
}

func canonicalSourceName(name string) string {
	name = strings.TrimSpace(name)
	lower := strings.ToLower(name)
	if lower == "kernel" {
		return "kernel"
	}
	if lower == "linux" || lower == "linux-kvm" || lower == "linux-raspi" || lower == "linux-ibm" ||
		lower == "linux-riscv" || lower == "linux-nvidia" || lower == "linux-realtime" || lower == "linux-intel" ||
		strings.HasPrefix(lower, "linux-aws-") || strings.HasPrefix(lower, "linux-azure-") ||
		strings.HasPrefix(lower, "linux-gcp-") || strings.HasPrefix(lower, "linux-oracle-") ||
		strings.HasPrefix(lower, "linux-hwe-") || strings.HasPrefix(lower, "linux-ibm-") ||
		strings.HasPrefix(lower, "linux-intel-") || strings.HasPrefix(lower, "linux-kvm-") ||
		strings.HasPrefix(lower, "linux-lowlatency") || strings.HasPrefix(lower, "linux-meta-") ||
		strings.HasPrefix(lower, "linux-nvidia") || strings.HasPrefix(lower, "linux-realtime-") ||
		strings.HasPrefix(lower, "linux-riscv-") || strings.HasPrefix(lower, "linux-raspi-") ||
		strings.HasPrefix(lower, "linux-signed-") || strings.HasPrefix(lower, "linux-restricted-modules-") {
		return "linux"
	}
	return name
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
