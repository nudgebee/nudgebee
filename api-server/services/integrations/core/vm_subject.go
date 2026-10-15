package core

import (
	"strings"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// vmPlaceholderPrefix is the resourse_id prefix of a VM the inventory knows only by IP.
const vmPlaceholderPrefix = "vm-"

// instanceIDLabels carry the cloud instance id when the rule kept it
// (node_cloud_info exposes it as `instance_id`).
var instanceIDLabels = []string{"instance_id", "cloud_instance_id"}

// VMSubjectInput is the subset of an event the VM lookup needs.
type VMSubjectInput struct {
	Labels map[string]string
}

// VMSubject is the VM inventory row an alert is about.
type VMSubject struct {
	CloudResourceID string
	Name            string
}

// ResolveVMSubject finds the VM an alert is about in a VM account's inventory,
// by cloud instance id, or by the host of the `instance` label when the alert
// carries no instance id. An instance id that is not in inventory is a miss, not a
// reason to fall back to whichever VM now holds the scrape IP. ok is false for a
// non-VM account, a miss, or an alert carrying neither identifier.
//
// The node-agent `instance` label is a hash of machine id and system UUID, so it
// only resolves when the rule also kept `instance_id` or the series is scraped
// by IP.
func ResolveVMSubject(sc *security.RequestContext, accountId string, in VMSubjectInput) (VMSubject, bool) {
	if sc == nil || sc.GetSecurityContext() == nil || accountId == "" {
		return VMSubject{}, false
	}
	instanceID := firstLabel(in.Labels, instanceIDLabels)
	host := instanceHost(in.Labels)
	if instanceID == "" && host == "" {
		return VMSubject{}, false
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return VMSubject{}, false
	}

	tenantId := sc.GetSecurityContext().GetTenantId()
	var row struct {
		ID   string `db:"id"`
		Name string `db:"name"`
	}
	err = dbms.Db.Get(&row, `
		SELECT cr.id::text AS id, cr.name
		FROM cloud_resourses cr
		JOIN cloud_accounts ca ON ca.id = cr.account
		WHERE cr.tenant = $1
		  AND cr.account = $2
		  AND ca.account_type = 'vm'
		  AND cr.is_active IS TRUE
		  AND cr.type IN ('VM', 'compute-instance')
		  AND (CASE WHEN $3 <> ''
		            THEN cr.resourse_id = $3
		            ELSE cr.resourse_id = $4
		                 OR cr.meta->>'PrivateIpAddress' = $5
		                 OR cr.meta->>'PublicIpAddress' = $5
		                 OR cr.meta->>'private_ip' = $5
		                 OR cr.meta->>'public_ip' = $5
		       END)
		ORDER BY cr.last_seen DESC NULLS LAST
		LIMIT 1`, tenantId, accountId, instanceID, vmPlaceholderPrefix+host, host)
	if err != nil {
		common.MetricsSubjectResolution(sc.GetContext(), "prometheus", "vm_subject", "unresolved", tenantId)
		return VMSubject{}, false
	}
	common.MetricsSubjectResolution(sc.GetContext(), "prometheus", "vm_subject", "resolved", tenantId)
	return VMSubject{CloudResourceID: row.ID, Name: row.Name}, true
}

func firstLabel(labels map[string]string, keys []string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(labels[k]); v != "" {
			return v
		}
	}
	return ""
}
