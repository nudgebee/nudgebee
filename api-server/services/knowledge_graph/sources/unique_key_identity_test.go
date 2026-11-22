package sources

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// The bug these tests pin: UserAccount nodes used to key on the editable, non-unique
// display name, so two people sharing one display name collapsed into a single node
// and a profile rename produced a second node (#41505, #37011).

func TestGenerateUniqueKey_UserAccountKeysOnUsername(t *testing.T) {
	b := NewBaseSource("gitlab")
	node := &core.DbNode{
		NodeType:       core.NodeTypeUserAccount,
		CloudAccountID: "acct-1",
		Properties: map[string]interface{}{
			"name":     "Mangglesh Dagar",
			"username": "mangglesh1653",
		},
	}

	got := b.GenerateUniqueKey(node)
	want := "gitlab:acct-1::UserAccount::mangglesh1653"
	if got != want {
		t.Errorf("GenerateUniqueKey() = %q, want %q", got, want)
	}
}

func TestGenerateUniqueKey_SharedDisplayNameStaysTwoNodes(t *testing.T) {
	b := NewBaseSource("gitlab")
	newUser := func(username string) *core.DbNode {
		return &core.DbNode{
			NodeType:       core.NodeTypeUserAccount,
			CloudAccountID: "acct-1",
			Properties: map[string]interface{}{
				// Two different people who happen to share a display name — the
				// case that used to merge them into one node.
				"name":     "Alex Kim",
				"username": username,
			},
		}
	}

	first := b.GenerateUniqueKey(newUser("alex.kim"))
	second := b.GenerateUniqueKey(newUser("akim2"))
	if first == second {
		t.Fatalf("two users sharing a display name produced one key: %q", first)
	}
}

func TestGenerateUniqueKey_DisplayNameChangeKeepsOneNode(t *testing.T) {
	b := NewBaseSource("gitlab")
	newUser := func(displayName string) *core.DbNode {
		return &core.DbNode{
			NodeType:       core.NodeTypeUserAccount,
			CloudAccountID: "acct-1",
			Properties: map[string]interface{}{
				"name":     displayName,
				"username": "mangglesh.dagar",
			},
		}
	}

	before := b.GenerateUniqueKey(newUser("mangglesh dagar"))
	after := b.GenerateUniqueKey(newUser("Mangglesh Dagar"))
	if before != after {
		t.Errorf("a display-name edit re-keyed the node: %q -> %q", before, after)
	}
}

func TestGenerateUniqueKey_UserAccountFallsBackToDisplayName(t *testing.T) {
	b := NewBaseSource("pagerduty")
	node := &core.DbNode{
		NodeType:       core.NodeTypeUserAccount,
		CloudAccountID: "acct-1",
		Properties: map[string]interface{}{
			"name": "Jane Doe",
		},
	}

	got := b.GenerateUniqueKey(node)
	want := "pagerduty:acct-1::UserAccount::Jane Doe"
	if got != want {
		t.Errorf("GenerateUniqueKey() with no username = %q, want %q", got, want)
	}
}

func TestGenerateUniqueKey_NonIdentityNodesStillKeyOnName(t *testing.T) {
	b := NewBaseSource("gitlab")
	node := &core.DbNode{
		NodeType:       core.NodeTypeRepository,
		CloudAccountID: "acct-1",
		Properties: map[string]interface{}{
			"name":     "widgets",
			"username": "should-be-ignored",
		},
	}

	got := b.GenerateUniqueKey(node)
	want := "gitlab:acct-1::Repository::widgets"
	if got != want {
		t.Errorf("GenerateUniqueKey() = %q, want %q", got, want)
	}
}
