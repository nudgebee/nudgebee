package integrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The Zenduty-service subject fallback is gated on the name being shaped like a
// service identifier rather than an org grouping. That shape check is the only
// part of resolveZendutySubjectFromService that is decidable without a database,
// and it is what keeps the "Database Team" leak (TestZenduty_CustomPayload_
// PreventsServiceNameLeak) closed on accounts with no workload inventory.
//
// Service names below are the real values carried by an in-house alerting stack
// routed through Zenduty, whose alerts carry no Alertmanager labels block at all.
func TestZendutyServiceSubject_ShapeGate(t *testing.T) {
	accepted := []string{
		"prod-unicorn-app",
		"dorado-grocery",
		"dorado-shopsy-fk",
		"lockin-falcon-prod",
		"prod-hyd-cosmos",
		"prod-transactmonitor",
	}
	for _, name := range accepted {
		assert.True(t, reServiceNamePattern.MatchString(name),
			"%q is a service slug and must be eligible as a last-resort subject", name)
	}

	// Org groupings — a Zenduty "service" is frequently a team, not the alert
	// subject. These must stay unresolved so the LLM fallback keeps its chance.
	rejected := []string{
		"Database Team",
		"Sample Service",
		"Payments Team",
		"Platform",   // single token — no hyphen, indistinguishable from prose
		"prod_db_01", // underscores are not k8s-style names
	}
	for _, name := range rejected {
		assert.False(t, reServiceNamePattern.MatchString(name),
			"%q is an org grouping / non-service name and must NOT become a subject", name)
	}
}
