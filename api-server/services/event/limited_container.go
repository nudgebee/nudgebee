package event

import (
	"log/slog"
	"strings"

	"nudgebee/services/internal/database"
)

// SingleLimitedContainer returns the one container of a workload that sets a limit on
// resource ("cpu" or "memory"), or "" when none or several do. A resize fix changes
// one container, so a workload-wide alert can name a container only when the answer
// is unambiguous.
//
// It reads the workload inventory, whose container specs carry their resources. The
// pod inventory does not: its containers hold only a name and an image, so a lookup
// there never finds a limit.
func SingleLimitedContainer(accountId, namespace, kind, workload, resource string) string {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return ""
	}
	var names []string
	err = dbms.Db.Select(&names, `SELECT DISTINCT c->>'name' FROM k8s_workloads w,
		  jsonb_array_elements(CASE WHEN jsonb_typeof(w.meta->'config'->'containers') = 'array'
		                            THEN w.meta->'config'->'containers' ELSE '[]'::jsonb END) c
		WHERE w.cloud_account_id = $1 AND w.namespace = $2 AND w."name" = $3
		  AND lower(w.kind) = $4 AND w.is_active IS NOT FALSE
		  AND COALESCE(c->'resources'->'limits'->>$5, '') <> ''`,
		accountId, namespace, workload, strings.ToLower(kind), resource)
	if err != nil {
		slog.Warn("event: could not read the workload's containers", "error", err, "workload", workload, "namespace", namespace)
		return ""
	}
	if len(names) != 1 {
		return ""
	}
	return names[0]
}
