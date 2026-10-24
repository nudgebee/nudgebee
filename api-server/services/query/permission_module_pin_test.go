package query

import (
	"sort"
	"strings"
	"testing"

	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
)

// A grantable /rpc/query action is authorized by its table's PermissionModule.
// The gateway admits the caller on the action's grant key, so the two must name
// the same module, or a holder of exactly the grant the role editor offers is
// admitted and then denied (no module) or checked against another module's grant.
// tenantlessGrantableTables are grantable query tables with no tenant column.
// The engine applies no tenant or account filter to such a table (query/service.go
// skips the whole restriction block), so a module would change nothing and
// every caller the gateway admits reads all of it. Each must be reviewed as
// safe to read across tenants, or made non-grantable.
var tenantlessGrantableTables = map[string]string{
	"anomaly_type_v2":           "global catalog of anomaly types",
	"cloud_resource_details_v2": "global catalog of cloud resource types",
	"feature_v2":                "global catalog of product features",
	"knowledge_base_v2":         "global knowledge-base rules",
	"user_details_v2":           "KNOWN GAP: any built-in role reads another tenant's user profile by id; follow-up",
}

func TestQueryTablesCarryTheirActionsModule(t *testing.T) {
	var missing, drifted, tenantless []string
	for action, key := range security.ActionKeys() {
		def, ok := GetTableMetadata(action)
		if !ok {
			continue // not a query-engine action
		}
		if def.TenantIdColumnName == "" {
			if _, reviewed := tenantlessGrantableTables[def.Name]; !reviewed {
				tenantless = append(tenantless, action+" ("+key+", table "+def.Name+")")
			}
			continue
		}
		module, _, _ := strings.Cut(key, ":")
		switch def.PermissionModule {
		case module:
		case "":
			missing = append(missing, action+" ("+key+")")
		default:
			drifted = append(drifted, action+" ("+key+", table "+def.PermissionModule+")")
		}
	}
	sort.Strings(missing)
	sort.Strings(drifted)
	assert.Empty(t, missing, "query tables with no PermissionModule: grantable at the gateway, denied by the engine")
	assert.Empty(t, drifted, "query tables whose PermissionModule differs from the action's grant key")
	sort.Strings(tenantless)
	assert.Empty(t, tenantless, "grantable query tables with no tenant column: review in tenantlessGrantableTables or make the action non-grantable")
}
