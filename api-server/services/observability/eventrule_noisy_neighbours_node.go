package observability

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/internal/database"
)

// noisyNeighboursNodeName resolves the node whose neighbours we should look at.
//
// Agent-sourced events (OOMKill, CrashLoopBackOff) carry the node on the event.
// Alert-sourced ones do not: a week of production CPUThrottlingHigh,
// KubePodNotReady and KubeContainerWaiting events — 3589 of them — carried a
// pod, a namespace and a container, and never a node or an instance label. So
// for exactly the events that suggest contention, the enricher used to disable
// itself. We fall back to the pod inventory, which the k8s collector keeps
// current and which records the node for 99.99% of pods.
func noisyNeighboursNodeName(ctx playbooks.PlaybookActionContext) string {
	if n := playbooks.SubjectNodeName(ctx.GetEvent()); n != "" {
		return n
	}
	podName, namespace := playbooks.SubjectPodNamespace(ctx.GetEvent())
	if podName == "" || namespace == "" {
		return ""
	}
	return lookupPodNode(ctx, podName, namespace)
}

// lookupPodNode reads the node from the K8s pod inventory. Hits
// idx_cloud_resourses_active_account_type / idx_cloud_resourses_account_type_name,
// so it is a single indexed row read, and it only runs for the handful of
// aggregation keys this enricher accepts.
//
// A miss is not an error: the pod may have been deleted, or belong to a cluster
// whose inventory has not caught up. The caller treats "" as "cannot enrich",
// which is what it did for these events before anyway.
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
		   AND is_active = true
		   AND meta->>'node' IS NOT NULL
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
