package agents

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/llm/events"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// An event found by id on another account is summarised with that account's
// cloud, not the conversation's.
func TestEventSummaryDescribesTheEventsAccount(t *testing.T) {
	ctx := toolcore.NbToolContext{Ctx: security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"dev-id", "prod-id"}), AccountId: "dev-id"}
	devOnly := toolcore.NbToolContext{Ctx: security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"dev-id"}), AccountId: "dev-id"}
	prod := []events.Event{{CloudAccountId: "prod-id"}, {CloudAccountId: "prod-id"}}

	assert.Equal(t, "prod-id", summaryAccountID(ctx, prod))
	assert.Equal(t, "dev-id", summaryAccountID(devOnly, prod), "an account the caller cannot read is not used")
	assert.Equal(t, "dev-id", summaryAccountID(ctx, []events.Event{{CloudAccountId: "prod-id"}, {CloudAccountId: "dev-id"}}), "mixed accounts keep the call's")
	assert.Equal(t, "dev-id", summaryAccountID(ctx, []events.Event{{}}), "rows without the column keep the call's")
	assert.Equal(t, "dev-id", summaryAccountID(ctx, nil))
}
