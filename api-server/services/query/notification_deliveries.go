package query

import (
	"slices"
	"strings"

	"nudgebee/services/security"
)

// notificationDeliveriesDef is the readable part of sent_notifications, aliased as alias: one row per attempt
// to send a message to one destination. Rows the send path keeps as state (remediation markers and waiting
// posts, Nubi answer cards), the columns only that state uses, and rows written before the delivery columns
// existed are never selected. is_final marks a send's latest attempt to a destination, so lists and counts
// can be per send. With teamURL, team_url is the Slack workspace's URL saved with its integration, for
// message links.
func notificationDeliveriesDef(alias string, teamURL bool) string {
	var def strings.Builder
	def.WriteString(`(SELECT sn.id, sn.created_at, sn.tenant_id, sn.account_id, sn.fingerprint, sn.dispatch_id, sn.attempt,
		sn.kind, sn.source, sn.title, sn.event_id, sn.platform, sn.team_id, sn.destination, sn.destination_name,
		sn.message_id, sn.thread_id, sn.status, sn.reason, sn.error_code, sn.routed_by,
		NOT EXISTS (
			SELECT 1 FROM sent_notifications later
			WHERE later.dispatch_id = sn.dispatch_id AND later.attempt > sn.attempt
				AND later.platform IS NOT DISTINCT FROM sn.platform
				AND later.destination IS NOT DISTINCT FROM sn.destination
		) AS is_final`)
	if teamURL {
		def.WriteString(`,
		CASE WHEN sn.platform = 'slack' THEN (
			SELECT icv.value FROM integrations i
			JOIN integration_config_values icv ON icv.integration_id = i.id AND icv.name = 'team_url'
			WHERE i.tenant_id = sn.tenant_id AND i.type = 'slack' AND i.name = sn.team_id
			LIMIT 1
		) END AS team_url`)
	}
	def.WriteString(`
	FROM sent_notifications sn
	WHERE sn.kind IS NOT NULL AND sn.kind NOT IN ('remediation_marker', 'remediation_pending', 'nubi_answer')) AS `)
	def.WriteString(alias)
	return def.String()
}

// deliveriesModule is the permission module delivery history is read under: the alerts'.
const deliveriesModule = "events"

// deliveryReadFilters keeps email rows, which name their recipients, to tenant-wide admins. It also keeps
// a reader whose access to an account is limited to some of its namespaces out of that account's rows:
// deliveries carry no namespace, so they cannot be narrowed to the reader's.
func deliveryReadFilters(ctx *security.RequestContext, request QueryRequest) (QueryRequest, error) {
	caller := ctx.GetSecurityContext()
	if caller.IsSuperAdmin() || caller.IsTenantAdmin() || caller.IsTenantReadAdmin() {
		return request, nil
	}
	request.Where.And = append(request.Where.And, QueryWhereClause{
		Binary: BinaryWhereClause{"kind": {NLike: "email:%"}},
	})
	if caller.HasPermission(deliveriesModule, "Read") || caller.HasPermission(deliveriesModule, "Write") {
		return request, nil
	}
	roles := caller.GetRoles()
	if !slices.Contains(roles, security.AUTH_K8S_NAMESPACE_ADMIN_ROLE) && !slices.Contains(roles, security.AUTH_K8S_NAMESPACE_READ_ADMIN_ROLE) {
		return request, nil
	}
	var namespaceOnly []string
	for _, accountId := range caller.ListAccountIds() {
		if caller.HasScopedRole(security.AUTH_ACCOUNT_ADMIN_ROLE, accountId) ||
			caller.HasScopedRole(security.AUTH_ACCOUNT_READ_ADMIN_ROLE, accountId) ||
			caller.HasScopedPermission(accountId, deliveriesModule, "Read") ||
			caller.HasScopedPermission(accountId, deliveriesModule, "Write") {
			continue
		}
		namespaceOnly = append(namespaceOnly, accountId)
	}
	if len(namespaceOnly) > 0 {
		request.Where.And = append(request.Where.And, QueryWhereClause{
			Binary: BinaryWhereClause{"account_id": {NotIn: namespaceOnly}},
		})
	}
	return request, nil
}
