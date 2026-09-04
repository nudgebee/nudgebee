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
// Agent-sourced events (OOMKill, CrashLoopBackOff) carry the node on the event.
// Alert-sourced ones do not: a week of production CPUThrottlingHigh,
// KubePodNotReady and KubeContainerWaiting events — 3589 of them — carried a
// pod, a namespace and a container, and never a node or an instance label. So
// for exactly the events that suggest contention, the enricher used to disable
// itself. We fall back to the pod inventory, which the k8s collector keeps
// current and which records the node for 99.99% of pods.
func noisyNeighboursNodeName(ctx playbooks.PlaybookActionContext) string {
	event := ctx.GetEvent()
	if event.SubjectNode != "" {
		return event.SubjectNode
	}
	if strings.EqualFold(event.SubjectType, "node") && event.SubjectName != "" {
		return event.SubjectName
	}
	if n := event.Labels["node"]; n != "" {
		return n
	}

	// Inventory before any `instance` label. SubjectNodeName falls back to
	// `instance` last, which for a kube-state-metrics-sourced alert is the KSM
	// pod's scrape address, not a node: a live KubePodCrashLooping event on dev
	// carried instance="10.64.0.141:8080", every query filtered on a node by
	// that name matched nothing, and the card rendered as an all-zero node —
	// which reads as "this machine is idle" rather than "we could not tell".
	if podName, namespace := playbooks.SubjectPodNamespace(event); podName != "" && namespace != "" {
		if n := lookupPodNode(ctx, podName, namespace); n != "" {
			return n
		}
	}

	// Only now the instance label, and only when it could be a node name.
	if n := event.Labels["instance"]; looksLikeNodeName(n) {
		return n
	}
	return ""
}

// looksLikeNodeName rejects scrape-target addresses — "10.64.0.141:8080" —
// which alert labels carry in `instance` and which no node is called.
//
// A bare IP is allowed through: some clusters really do name their nodes by
// address, and refusing them would skip the card on exactly those clusters
// whenever the inventory lookup above missed. The asymmetry that used to make
// this dangerous is gone — if the value turns out not to be a node, every
// query returns empty and the enricher declines to render rather than
// reporting an idle machine.
func looksLikeNodeName(v string) bool {
	return v != "" && !strings.Contains(v, ":")
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
