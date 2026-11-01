package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTargetAccountIDDefaults(t *testing.T) {
	ctx := NewNbToolContext(nil, nil, "origin-id", "user", "conv", "msg", "agent", "", nil, "", NBQueryConfig{}, "call")
	require.Equal(t, "origin-id", ctx.TargetAccountId, "a call not about another account reads its own account")
	require.Equal(t, "origin-id", ctx.DataAccountID())

	handed := NBQueryConfig{DelegatedTarget: &ExecutionTarget{Provider: "kubernetes", Name: "k8s-prod", AccountID: "prod-id"}}
	ctx = NewNbToolContext(nil, nil, "origin-id", "user", "conv", "msg", "agent", "", nil, "", handed, "call")
	require.Equal(t, "prod-id", ctx.DataAccountID(), "a child handed a target reads the target's account")
	require.Equal(t, "origin-id", ctx.AccountId, "the conversation's account is unchanged")

	require.Equal(t, "origin-id", NbToolContext{AccountId: "origin-id"}.DataAccountID(), "a hand-built context falls back to its own account")
}
