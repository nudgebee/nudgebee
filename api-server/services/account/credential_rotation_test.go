package account

import (
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/internal/database/models"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFirstSubscription covers how the rotation handler picks a representative
// subscription to validate the new secret against: assume_role stores an
// account's subscription IDs comma-joined, and only the first is needed.
func TestFirstSubscription(t *testing.T) {
	assert.Equal(t, "sub-a", firstSubscription("sub-a"))
	assert.Equal(t, "sub-a", firstSubscription("sub-a,sub-b,sub-c"))
	assert.Equal(t, "sub-a", firstSubscription(" sub-a , sub-b "))
	assert.Equal(t, "", firstSubscription(""))
}

// TestAccountUpdateCredentialsRequest_Decodes guards the RPC payload contract:
// UnmarshalMapToStruct silently drops keys whose mapstructure tag doesn't match,
// so a renamed field would make the rotation a no-op with no error — the same
// failure class as the account_env picker regression.
func TestAccountUpdateCredentialsRequest_Decodes(t *testing.T) {
	var request AccountUpdateCredentialsRequest
	err := common.UnmarshalMapToStruct(map[string]any{
		"id":             "acc-123",
		"cloud_provider": "Azure",
		"client_secret":  "new-secret-value",
	}, &request)

	assert.NoError(t, err)
	assert.Equal(t, "acc-123", request.Id)
	assert.Equal(t, "Azure", request.CloudProvider)
	assert.Equal(t, "new-secret-value", request.ClientSecret)
}

// TestCredentialRotationTargets covers the fan-out: every account on the app
// registration the caller may update gets the new secret, the starting account
// always does, and accounts the caller can't update are left alone.
func TestCredentialRotationTargets(t *testing.T) {
	group := []credentialGroupMember{
		{Id: "a", Status: "active"},
		{Id: "b", Status: "active"},
		{Id: "c", Status: "disabled"},
		{Id: "d", Status: "active"},
	}
	canUpdate := func(id string) bool { return id != "d" }

	targets := credentialRotationTargets("a", group, canUpdate)
	assert.Equal(t, []credentialGroupMember{
		{Id: "a", Status: "active"},
		{Id: "b", Status: "active"},
		{Id: "c", Status: "disabled"},
	}, targets)

	// The starting account was access-checked before validation, so it is
	// written even when the per-account check below would say no.
	targets = credentialRotationTargets("d", group, func(string) bool { return false })
	assert.Equal(t, []credentialGroupMember{{Id: "d", Status: "active"}}, targets)

	// A group query that missed the starting account still rotates it.
	targets = credentialRotationTargets("z", nil, canUpdate)
	assert.Equal(t, []credentialGroupMember{{Id: "z", Status: "active"}}, targets)
}

// TestActiveTargets: only accounts that were actually updated and are active get
// their collection restarted.
func TestActiveTargets(t *testing.T) {
	targets := []credentialGroupMember{
		{Id: "a", Status: "active"},
		{Id: "b", Status: "disabled"},
		{Id: "c", Status: "active"},
	}
	assert.Equal(t, []string{"a"}, activeTargets(targets, []string{"a", "b"}))
	assert.Empty(t, activeTargets(targets, nil))
}

// TestAccountUpdateCredentialsRequest_RejectsMalformedId: a non-UUID id fails
// validation (400) instead of reaching the uuid[] cast in SQL.
func TestAccountUpdateCredentialsRequest_RejectsMalformedId(t *testing.T) {
	err := common.ValidateStruct(AccountUpdateCredentialsRequest{Id: "not-a-uuid", CloudProvider: "Azure", ClientSecret: "s"})
	assert.Error(t, err)
	err = common.ValidateStruct(AccountUpdateCredentialsRequest{Id: "00000000-0000-0000-0000-00000000000a", CloudProvider: "Azure", ClientSecret: "s"})
	assert.NoError(t, err)
}

// TestAccountUpdateCredentialsRequest_DecodesGcpKey guards the GCP payload
// field the same way: a dropped credentials_json would make rotation refuse
// with "key is required" instead of storing the key.
func TestAccountUpdateCredentialsRequest_DecodesGcpKey(t *testing.T) {
	var request AccountUpdateCredentialsRequest
	err := common.UnmarshalMapToStruct(map[string]any{
		"id":               "00000000-0000-0000-0000-00000000000a",
		"cloud_provider":   "GCP",
		"credentials_json": `{"type":"service_account"}`,
	}, &request)
	assert.NoError(t, err)
	assert.Equal(t, `{"type":"service_account"}`, request.CredentialsJSON)
}

func TestParseGcpServiceAccountKey(t *testing.T) {
	key, err := parseGcpServiceAccountKey(`{"type":"service_account","project_id":"p1","client_email":"sa@p1.iam.gserviceaccount.com","private_key":"-----BEGIN PRIVATE KEY-----"}`)
	assert.NoError(t, err)
	assert.Equal(t, "sa@p1.iam.gserviceaccount.com", key.ClientEmail)

	_, err = parseGcpServiceAccountKey(`not json`)
	assert.ErrorContains(t, err, "not valid JSON")
	_, err = parseGcpServiceAccountKey(`{"type":"authorized_user","project_id":"p1","client_email":"a","private_key":"k"}`)
	assert.ErrorContains(t, err, "not a service-account key")
	_, err = parseGcpServiceAccountKey(`{"type":"service_account","project_id":"p1","client_email":"sa@p1.iam.gserviceaccount.com"}`)
	assert.ErrorContains(t, err, "missing")
}

func TestGcpBillingConfig(t *testing.T) {
	data := `{"project_id":"p1","billing_data":{"billing_project_id":"bp","dataset_name":"ds","table_name":"tbl"}}`
	p, d, tb := gcpBillingConfig(&data)
	assert.Equal(t, []string{"bp", "ds", "tbl"}, []string{p, d, tb})

	none := `{"project_id":"p1"}`
	p, d, tb = gcpBillingConfig(&none)
	assert.Equal(t, []string{"", "", ""}, []string{p, d, tb})
	p, d, tb = gcpBillingConfig(nil)
	assert.Equal(t, []string{"", "", ""}, []string{p, d, tb})
}

// TestStoredGcpServiceAccountEmail round-trips a key through the same
// encryption the account rows use, so grouping reads exactly what onboarding
// stored.
func TestStoredGcpServiceAccountEmail(t *testing.T) {
	previous := config.Config.NudgebeeEncryptionKey
	config.Config.NudgebeeEncryptionKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	t.Cleanup(func() { config.Config.NudgebeeEncryptionKey = previous })

	stored, err := common.Encrypt(`{"type":"service_account","client_email":"sa@p1.iam.gserviceaccount.com"}`)
	assert.NoError(t, err)
	email, err := storedGcpServiceAccountEmail(&stored)
	assert.NoError(t, err)
	assert.Equal(t, "sa@p1.iam.gserviceaccount.com", email)

	_, err = storedGcpServiceAccountEmail(nil)
	assert.Error(t, err)
	garbage := "zz"
	_, err = storedGcpServiceAccountEmail(&garbage)
	assert.Error(t, err)
}

func TestAwsRoleArnAccount(t *testing.T) {
	account, ok := awsRoleArnAccount("arn:aws:iam::123456789012:role/nudgebee-CrossAccountRole-ABC")
	assert.True(t, ok)
	assert.Equal(t, "123456789012", account)

	account, ok = awsRoleArnAccount("arn:aws-us-gov:iam::210987654321:role/path/to/role")
	assert.True(t, ok)
	assert.Equal(t, "210987654321", account)

	account, ok = awsRoleArnAccount("arn:aws-iso-b:iam::210987654321:role/r")
	assert.True(t, ok)
	assert.Equal(t, "210987654321", account)

	for _, bad := range []string{"", "nudgebee-role", "arn:aws:iam::12345:role/x", "arn:aws:iam::123456789012:user/alice", "arn:aws:iam::123456789012:role/", "arn:awsfoo:iam::123456789012:role/x", "arn:aws-:iam::123456789012:role/x"} {
		_, ok = awsRoleArnAccount(bad)
		assert.False(t, ok, bad)
	}
}

func TestAwsCurConfig(t *testing.T) {
	data := `{"cost_report_name":"nb-cur","cost_report_s3_bucket":"nb-cur-bucket","cur_source":"access_keys"}`
	report, bucket := awsCurConfig(&data)
	assert.Equal(t, []string{"nb-cur", "nb-cur-bucket"}, []string{report, bucket})
	report, bucket = awsCurConfig(nil)
	assert.Equal(t, []string{"", ""}, []string{report, bucket})
}

func TestAccountUpdateCredentialsRequest_DecodesAwsFields(t *testing.T) {
	var request AccountUpdateCredentialsRequest
	err := common.UnmarshalMapToStruct(map[string]any{
		"id":             "00000000-0000-0000-0000-00000000000a",
		"cloud_provider": "AWS",
		"assume_role":    "arn:aws:iam::123456789012:role/r",
		"access_key":     "AKIAEXAMPLE",
		"access_secret":  "s3cr3t",
		"validate_only":  true,
	}, &request)
	assert.NoError(t, err)
	assert.Equal(t, "arn:aws:iam::123456789012:role/r", request.AssumeRole)
	assert.Equal(t, "AKIAEXAMPLE", request.AccessKey)
	assert.Equal(t, "s3cr3t", request.AccessSecret)
	assert.True(t, request.ValidateOnly)
}

// TestPrepareAwsRotation_RefusesBeforeAnyCall covers the refusals that must
// happen before any AWS call or database read: a mixed account, a type switch,
// and a role in another AWS account (so the stored external ID is never sent
// there). nil context and database prove nothing past the checks is reached.
func TestPrepareAwsRotation_RefusesBeforeAnyCall(t *testing.T) {
	str := func(s string) *string { return &s }
	roleAcct := models.Account{AccountNumber: str("222222222222"), AssumeRole: str("arn:aws:iam::222222222222:role/old"), ExternalId: str("ext")}
	keyAcct := models.Account{AccountNumber: str("111111111111"), AccessKey: str("AKIAOLD"), AccessSecret: str("enc")}
	mixed := models.Account{AccountNumber: str("111111111111"), AssumeRole: str("arn:aws:iam::111111111111:role/r"), AccessKey: str("AKIAOLD")}

	cases := []struct {
		name    string
		acct    models.Account
		request AccountUpdateCredentialsRequest
		want    string
	}{
		{"mixed account", mixed, AccountUpdateCredentialsRequest{AssumeRole: "arn:aws:iam::111111111111:role/new"}, "both an IAM role and access keys"},
		{"keys on a role account", roleAcct, AccountUpdateCredentialsRequest{AccessKey: "AKIANEW", AccessSecret: "s"}, "connects with an IAM role"},
		{"role on a key account", keyAcct, AccountUpdateCredentialsRequest{AssumeRole: "arn:aws:iam::111111111111:role/new"}, "connects with access keys"},
		{"role in another account", roleAcct, AccountUpdateCredentialsRequest{AssumeRole: "arn:aws:iam::333333333333:role/x"}, "belongs to AWS account 333333333333"},
		{"not a role ARN", roleAcct, AccountUpdateCredentialsRequest{AssumeRole: "arn:aws:iam::222222222222:user/alice"}, "is not an IAM role ARN"},
		{"half a key pair", keyAcct, AccountUpdateCredentialsRequest{AccessKey: "AKIANEW"}, "access key ID and secret access key are required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := prepareAwsRotation(nil, nil, "tenant", c.acct, c.request)
			assert.ErrorContains(t, err, c.want)
		})
	}
}
