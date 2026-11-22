package flow_sources

import (
	"strings"
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

func TestPrepareRole(t *testing.T) {
	cases := []struct {
		name       string
		nodeType   string
		role       string
		engine     string
		wantErr    string
		wantRole   string
		wantEngine string
	}{
		{name: "no role", nodeType: "Database"},
		{name: "role and engine", nodeType: "ComputeInstance", role: "database", engine: " PostgreSQL ", wantRole: "database", wantEngine: "postgresql"},
		{name: "workload", nodeType: "Workload", role: "cache", wantRole: "cache"},
		{name: "unknown role", nodeType: "Service", role: "db", wantErr: "unknown role"},
		{name: "engine without role", nodeType: "Service", engine: "redis", wantErr: "needs a role"},
		{name: "bad engine", nodeType: "Service", role: "cache", engine: "my redis", wantErr: "engine"},
		{name: "type that cannot carry a role", nodeType: "Namespace", role: "database", wantErr: "cannot carry a role"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := &ManualNode{NodeType: c.nodeType}
			err := prepareRole(n, ManualNodeCreate{Role: c.role, Engine: c.engine})
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if n.Role != c.wantRole || n.Engine != c.wantEngine {
				t.Errorf("role/engine = %q/%q, want %q/%q", n.Role, n.Engine, c.wantRole, c.wantEngine)
			}
		})
	}
}

func TestCustomKindRefusesRoleBookkeepingKeys(t *testing.T) {
	for _, k := range []string{core.PropRoleSource, core.PropDetectedRole, core.PropDetectedEngine} {
		err := validateCustomKindProperties(map[string]interface{}{k: "x"})
		if err == nil || !strings.Contains(err.Error(), "set the role instead") {
			t.Errorf("%s: err = %v, want a refusal", k, err)
		}
	}
	// A switch describing itself with a free "role" keeps working.
	if err := validateCustomKindProperties(map[string]interface{}{core.PropRole: "core-switch", core.PropEngine: "nx-os"}); err != nil {
		t.Errorf("free role/engine on a custom kind must stay allowed: %v", err)
	}
}
