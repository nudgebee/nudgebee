package observability

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/internal/database"
)

// noisyNeighboursNodeName resolves the node whose neighbours we should look at.
//
// For a pod-subject event we read it out of the pod's own inventory row. That
// is the only source that records which node a pod is actually running on; the
// alert's labels do not carry it. `instance` looks like it might, but for a
// kube-state-metrics-sourced alert it is the KSM pod's scrape address —
// "10.64.21.224:8080" — and where a relabelling rule has stripped the port,
// a bare IP. Neither is a node name: of 25,341 node records in the inventory,
// none is named by address, so such a value cannot match the `node=` label any
// query here builds. Deriving the node from event fields at all was the bug —
// KubePodCrashLooping produced 0 cards from 124 events because it carried a
// scrape address and every query filtered on a node by that name matched
// nothing (#37669).
//
// Ask the database for the pod, take the node off the row.
func noisyNeighboursNodeName(ctx playbooks.PlaybookActionContext) string {
	event := ctx.GetEvent()

	if podName, namespace := playbooks.SubjectPodNamespace(event); podName != "" && namespace != "" {
		if n := lookupPodNode(ctx, podName, namespace); n != "" {
			return n
		}
	}

	// A node-subject event names the node directly, so there is no pod to look
	// up. This is the node_not_ready path, which never had the address problem.
	if strings.EqualFold(event.SubjectType, "node") && event.SubjectName != "" {
		return event.SubjectName
	}

	// Last resort for a pod the inventory has never seen. Reached only when the
	// lookup above found nothing, and safe because ingest blanks an address
	// before it is stored (event.normalizeSubjectNode) — an agent-sourced value
	// here is obj.spec.nodeName, which is a real node name.
	return event.SubjectNode
}

// lookupPodNode reads the node from the K8s pod inventory. Hits
// idx_cloud_resourses_account_type_name (account, type, name), which is not
// partial, so it is a single indexed row read and it only runs for the handful
// of aggregation keys this enricher accepts.
//
// Deliberately not filtered on is_active. That filter looks like "the pod still
// exists" but does not behave like it: measured on test, for events under two
// hours old it matched only 21.5% of the time, while the same lookup without it
// resolved a node for 100% of 534 events across a 36h window. It is also not a
// safety filter — over 274 recent events, whenever an is_active row did exist
// the row chosen here named the same node, 0 disagreements — so removing it
// never changes an answer that already worked, it only adds the ones that were
// being dropped. is_active DESC still prefers a live row, updated_at DESC then
// takes the most recently seen, and the inventory is refreshed continuously
// (rows for these events were newer than the event itself).
//
// A miss is not an error: the pod may belong to a cluster whose inventory has
// not caught up. The caller falls back to whatever the event carried.
const podNodeLookupTimeout = 5 * time.Second

func lookupPodNode(ctx playbooks.PlaybookActionContext, podName, namespace string) string {
	accountID := ctx.GetAccountId()
	if accountID == "" {
		return ""
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Warn("noisy_neighbours: cannot reach metastore to resolve node", "error", err)
		return ""
	}

	const query = `
		SELECT meta->>'node'
		  FROM cloud_resourses
		 WHERE account = $1
		   AND type = 'Pod'
		   AND name = $2
		   AND meta->>'namespace' = $3
		   AND meta->>'node' IS NOT NULL
		 ORDER BY is_active DESC, updated_at DESC
		 LIMIT 1`

	// Bounded: this runs inside event enrichment, so a query that hangs would
	// hold up the whole card rather than just losing the node name.
	queryCtx, cancel := context.WithTimeout(context.Background(), podNodeLookupTimeout)
	defer cancel()

	var node sql.NullString
	if err := dbms.Db.QueryRowxContext(queryCtx, query, accountID, podName, namespace).Scan(&node); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			ctx.GetLogger().Warn("noisy_neighbours: node lookup failed",
				"error", err, "pod", podName, "namespace", namespace)
		}
		return ""
	}
	return node.String
}
