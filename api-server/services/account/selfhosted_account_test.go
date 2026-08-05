package account

import (
	"nudgebee/services/common"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A self-hosted VM fleet is reached the same way a Kubernetes cluster is — an
// agent inside the customer's network dialling out to the relay — but there is
// no cloud account behind it. Before the `vm` account type existed, such a
// request fell through CreateAccount's provider branches to "unknown cloud
// provider", which is why VM fleets are currently onboarded as Kubernetes
// accounts (#35683).
//
// These tests pin the decode contract the UI relies on. The credential-issuing
// branch itself needs a database and is covered by the account integration
// suite.

func TestAccountCreateRequest_DecodesSelfHostedProvider(t *testing.T) {
	var request AccountCreateRequest
	err := common.UnmarshalMapToStruct(map[string]any{
		"account_name":   "on-prem-fleet",
		"cloud_provider": AccountProviderSelfHosted,
		"account_type":   AccountTypeVM,
	}, &request)

	assert.NoError(t, err)
	assert.Equal(t, AccountProviderSelfHosted, request.CloudProvider)
	assert.Equal(t, AccountTypeVM, request.AccountType)
}

// A self-hosted account must not require any of the cloud credential fields.
// Requiring them is what the onboarding form would otherwise inherit from the
// cloud flow, and there is nothing to put in them: no provider API, no billing
// source, no region.
func TestAccountCreateRequest_SelfHostedNeedsNoCloudCredentials(t *testing.T) {
	var request AccountCreateRequest
	err := common.UnmarshalMapToStruct(map[string]any{
		"account_name":   "on-prem-fleet",
		"cloud_provider": AccountProviderSelfHosted,
		"account_type":   AccountTypeVM,
	}, &request)

	assert.NoError(t, err)
	assert.Empty(t, request.AccessKey)
	assert.Empty(t, request.AccessSecret)
	assert.Empty(t, request.AssumeRole)
	assert.Empty(t, request.Region)
}

// The constants must stay distinct from the Kubernetes ones. Collapsing them
// would reintroduce exactly the conflation this change exists to remove — a VM
// fleet indistinguishable from a cluster in every account-scoped query.
//
// They must also stay distinct from *each other*: account_type says what is
// managed, cloud_provider says who runs it. Setting both to the same string
// makes one column redundant and leaves nowhere to record a VM fleet whose
// machines do sit in a cloud, discovered by an agent rather than the provider
// API.
func TestSelfHostedConstantsAreDistinct(t *testing.T) {
	assert.NotEqual(t, "kubernetes", AccountTypeVM)
	assert.NotEqual(t, "K8s", AccountProviderSelfHosted)
	assert.NotEqual(t, "cloud", AccountTypeVM)
	assert.NotEqual(t, AccountProviderSelfHosted, AccountTypeVM)
}

// A VM account issues no credentials of its own. A cluster has exactly one
// agent, so the kubernetes flow mints credentials with the account; a VM
// account holds many foragers, one per network segment, each credentialed by
// the VM agent integration alongside its install command. An account-level
// credential would be a second, unused identity implying a one-agent model we
// do not have.
func TestSelfHostedAccountIssuesNoAgentCredentials(t *testing.T) {
	var request AccountCreateRequest
	err := common.UnmarshalMapToStruct(map[string]any{
		"account_name":   "on-prem-fleet",
		"cloud_provider": AccountProviderSelfHosted,
		"account_type":   AccountTypeVM,
	}, &request)

	assert.NoError(t, err)
	assert.Empty(t, request.AgentAccessKey)
	assert.Empty(t, request.AgentAccessSecretV2)
}
