package account

import (
	"nudgebee/services/common"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAccountCreateRequest_DecodesAccountEnv guards the regression that made the
// environment picker a no-op: AccountCreateRequest had no account_env field, and
// UnmarshalMapToStruct silently drops unknown keys, so the value the UI sent never
// reached the INSERT and every account fell back to the column default.
func TestAccountCreateRequest_DecodesAccountEnv(t *testing.T) {
	var request AccountCreateRequest
	err := common.UnmarshalMapToStruct(map[string]any{
		"account_name":   "prod-account",
		"cloud_provider": "AWS",
		"account_env":    "prod",
	}, &request)

	assert.NoError(t, err)
	assert.Equal(t, "prod", request.AccountEnv)
}

// TestStructToInsertMap_AccountEnv covers both halves of the `,omitempty` contract.
// Present means the chosen value reaches the INSERT; absent means the column takes
// its 'non_prod' default. Emitting an empty string instead would violate the
// cloud_accounts_account_env_fkey constraint, since the empty string is not a
// row in account_env_type.
func TestStructToInsertMap_AccountEnv(t *testing.T) {
	t.Run("included when set", func(t *testing.T) {
		fields, err := structToInsertMap(AccountCreateRequest{
			AccountName:   "prod-account",
			CloudProvider: "AWS",
			AccountEnv:    "prod",
		})

		assert.NoError(t, err)
		assert.Equal(t, "prod", fields["account_env"])
	})

	t.Run("omitted when empty", func(t *testing.T) {
		fields, err := structToInsertMap(AccountCreateRequest{
			AccountName:   "some-account",
			CloudProvider: "AWS",
		})

		assert.NoError(t, err)
		assert.NotContains(t, fields, "account_env")
	})
}

func TestAccountCreateRequest_ValidatesAccountEnv(t *testing.T) {
	cases := []struct {
		name       string
		accountEnv string
		wantErr    bool
	}{
		{name: "prod", accountEnv: "prod"},
		{name: "non_prod", accountEnv: "non_prod"},
		{name: "empty falls back to column default", accountEnv: ""},
		{name: "legacy hyphenated value rejected", accountEnv: "non-prod", wantErr: true},
		{name: "arbitrary value rejected", accountEnv: "staging", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := common.ValidateStruct(AccountCreateRequest{
				AccountName:   "some-account",
				CloudProvider: "AWS",
				AccountEnv:    tc.accountEnv,
			})

			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAccountUpdateRequest_ValidatesAccountEnv(t *testing.T) {
	assert.NoError(t, common.ValidateStruct(AccountUpdateRequest{
		Id:         "11111111-1111-1111-1111-111111111111",
		AccountEnv: "prod",
	}))

	assert.Error(t, common.ValidateStruct(AccountUpdateRequest{
		Id:         "11111111-1111-1111-1111-111111111111",
		AccountEnv: "staging",
	}))
}
