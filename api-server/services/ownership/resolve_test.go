package ownership

import (
	"context"
	"testing"

	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
)

// fakeDeps backs the resolver with in-memory maps so resolution is testable
// without a database. resolveDeps is built entirely from function fields, so no
// mocking framework is needed.
type fakeDeps struct {
	owners     map[string]*ResourceOwnerRow  // keyed by ownerKey(resType, resKey)
	workloads  map[string]*workloadMeta      // keyed by cloud_resource_id
	cloudRes   map[string]*cloudResourceMeta // keyed by cloud_resourses.id
	activeNs   map[string]bool               // keyed by "<account>/<namespace>"
	rules      []OwnershipRuleRow
	cloudRules []OwnershipRuleRow
}

func ownerKey(resType, resKey string) string { return resType + "|" + resKey }

func (f fakeDeps) deps() resolveDeps {
	return resolveDeps{
		ownerOf:         func(t, k string) *ResourceOwnerRow { return f.owners[ownerKey(t, k)] },
		workload:        func(id string) *workloadMeta { return f.workloads[id] },
		cloudResource:   func(id string) *cloudResourceMeta { return f.cloudRes[id] },
		namespaceActive: func(key string) bool { return f.activeNs[key] },
		getRules:        func() []OwnershipRuleRow { return f.rules },
		getCloudRules:   func() []OwnershipRuleRow { return f.cloudRules },
	}
}

func ownerRow(ownerType, ownerID string) *ResourceOwnerRow {
	return &ResourceOwnerRow{OwnerType: ownerType, OwnerId: ownerID}
}

func activeApiWorkload() *workloadMeta {
	return &workloadMeta{CloudAccountId: "acct1", Namespace: "payments", Name: "api", Labels: map[string]string{"team": "payments"}}
}

func TestResolveWorkload(t *testing.T) {
	const wlKey = "wl1"

	t.Run("gone or inactive workload yields no owner (orphan guard)", func(t *testing.T) {
		f := fakeDeps{} // workloads map empty → workload(wlKey) == nil
		got := resolveOne(f.deps(), ResourceTypeWorkload, wlKey)
		assert.False(t, got.Found)
	})

	t.Run("direct manual owner", func(t *testing.T) {
		f := fakeDeps{
			workloads: map[string]*workloadMeta{wlKey: activeApiWorkload()},
			owners:    map[string]*ResourceOwnerRow{ownerKey(ResourceTypeWorkload, wlKey): ownerRow(OwnerTypeUser, "u1")},
		}
		got := resolveOne(f.deps(), ResourceTypeWorkload, wlKey)
		assert.True(t, got.Found)
		assert.Equal(t, OwnerTypeUser, got.OwnerType)
		assert.Equal(t, "u1", got.OwnerId)
		assert.Equal(t, SourceManual, got.Source)
		assert.Equal(t, ViaSelf, got.Via)
	})

	t.Run("rule when no manual owner", func(t *testing.T) {
		f := fakeDeps{
			workloads: map[string]*workloadMeta{wlKey: activeApiWorkload()},
			rules:     []OwnershipRuleRow{rule(MatchScopeLabel, "team", "payments", "", OwnerTypeGroup, "g1")},
		}
		got := resolveOne(f.deps(), ResourceTypeWorkload, wlKey)
		assert.True(t, got.Found)
		assert.Equal(t, OwnerTypeGroup, got.OwnerType)
		assert.Equal(t, "g1", got.OwnerId)
		assert.Equal(t, SourceRule, got.Source)
		assert.Equal(t, ViaSelf, got.Via)
	})

	t.Run("manual beats rule", func(t *testing.T) {
		f := fakeDeps{
			workloads: map[string]*workloadMeta{wlKey: activeApiWorkload()},
			owners:    map[string]*ResourceOwnerRow{ownerKey(ResourceTypeWorkload, wlKey): ownerRow(OwnerTypeUser, "manual")},
			rules:     []OwnershipRuleRow{rule(MatchScopeLabel, "team", "payments", "", OwnerTypeUser, "rule")},
		}
		got := resolveOne(f.deps(), ResourceTypeWorkload, wlKey)
		assert.Equal(t, "manual", got.OwnerId)
		assert.Equal(t, SourceManual, got.Source)
		assert.Equal(t, ViaSelf, got.Via)
	})

	t.Run("namespace rule resolves as inherited (via namespace), not self", func(t *testing.T) {
		f := fakeDeps{
			workloads: map[string]*workloadMeta{wlKey: activeApiWorkload()},
			rules:     []OwnershipRuleRow{rule(MatchScopeNamespace, "", "payments", "acct1", OwnerTypeGroup, "ns-rule")},
		}
		got := resolveOne(f.deps(), ResourceTypeWorkload, wlKey)
		assert.True(t, got.Found)
		assert.Equal(t, "ns-rule", got.OwnerId)
		assert.Equal(t, SourceRule, got.Source)
		assert.Equal(t, ViaNamespace, got.Via)
	})

	t.Run("manual namespace owner beats a namespace rule", func(t *testing.T) {
		f := fakeDeps{
			workloads: map[string]*workloadMeta{wlKey: activeApiWorkload()},
			owners:    map[string]*ResourceOwnerRow{ownerKey(ResourceTypeNamespace, "acct1/payments"): ownerRow(OwnerTypeUser, "ns-manual")},
			rules:     []OwnershipRuleRow{rule(MatchScopeNamespace, "", "payments", "acct1", OwnerTypeUser, "ns-rule")},
		}
		got := resolveOne(f.deps(), ResourceTypeWorkload, wlKey)
		assert.Equal(t, "ns-manual", got.OwnerId)
		assert.Equal(t, SourceManual, got.Source)
		assert.Equal(t, ViaNamespace, got.Via)
	})

	t.Run("label rule (workload-specific) beats a namespace rule", func(t *testing.T) {
		f := fakeDeps{
			workloads: map[string]*workloadMeta{wlKey: activeApiWorkload()},
			rules: []OwnershipRuleRow{
				rule(MatchScopeLabel, "team", "payments", "", OwnerTypeUser, "label-rule"),
				rule(MatchScopeNamespace, "", "payments", "acct1", OwnerTypeUser, "ns-rule"),
			},
		}
		got := resolveOne(f.deps(), ResourceTypeWorkload, wlKey)
		assert.Equal(t, "label-rule", got.OwnerId)
		assert.Equal(t, ViaSelf, got.Via)
	})

	t.Run("inherited from namespace owner", func(t *testing.T) {
		f := fakeDeps{
			workloads: map[string]*workloadMeta{wlKey: activeApiWorkload()},
			owners:    map[string]*ResourceOwnerRow{ownerKey(ResourceTypeNamespace, "acct1/payments"): ownerRow(OwnerTypeGroup, "ns-team")},
		}
		got := resolveOne(f.deps(), ResourceTypeWorkload, wlKey)
		assert.True(t, got.Found)
		assert.Equal(t, "ns-team", got.OwnerId)
		assert.Equal(t, SourceManual, got.Source)
		assert.Equal(t, ViaNamespace, got.Via)
	})

	t.Run("inherited from cloud account owner", func(t *testing.T) {
		f := fakeDeps{
			workloads: map[string]*workloadMeta{wlKey: activeApiWorkload()},
			owners:    map[string]*ResourceOwnerRow{ownerKey(ResourceTypeCloudAccount, "acct1"): ownerRow(OwnerTypeUser, "cluster-owner")},
		}
		got := resolveOne(f.deps(), ResourceTypeWorkload, wlKey)
		assert.True(t, got.Found)
		assert.Equal(t, "cluster-owner", got.OwnerId)
		assert.Equal(t, ViaCluster, got.Via)
	})

	t.Run("namespace owner preferred over cluster owner", func(t *testing.T) {
		f := fakeDeps{
			workloads: map[string]*workloadMeta{wlKey: activeApiWorkload()},
			owners: map[string]*ResourceOwnerRow{
				ownerKey(ResourceTypeNamespace, "acct1/payments"): ownerRow(OwnerTypeUser, "ns"),
				ownerKey(ResourceTypeCloudAccount, "acct1"):       ownerRow(OwnerTypeUser, "cluster"),
			},
		}
		got := resolveOne(f.deps(), ResourceTypeWorkload, wlKey)
		assert.Equal(t, "ns", got.OwnerId)
		assert.Equal(t, ViaNamespace, got.Via)
	})

	t.Run("no owner at any level", func(t *testing.T) {
		f := fakeDeps{workloads: map[string]*workloadMeta{wlKey: activeApiWorkload()}}
		got := resolveOne(f.deps(), ResourceTypeWorkload, wlKey)
		assert.False(t, got.Found)
	})
}

func TestResolveNamespace(t *testing.T) {
	const nsKey = "acct1/payments"
	active := map[string]bool{nsKey: true}

	t.Run("inactive namespace yields no owner", func(t *testing.T) {
		f := fakeDeps{activeNs: map[string]bool{}}
		got := resolveOne(f.deps(), ResourceTypeNamespace, nsKey)
		assert.False(t, got.Found)
	})

	t.Run("direct manual owner", func(t *testing.T) {
		f := fakeDeps{
			activeNs: active,
			owners:   map[string]*ResourceOwnerRow{ownerKey(ResourceTypeNamespace, nsKey): ownerRow(OwnerTypeUser, "u1")},
		}
		got := resolveOne(f.deps(), ResourceTypeNamespace, nsKey)
		assert.True(t, got.Found)
		assert.Equal(t, "u1", got.OwnerId)
		assert.Equal(t, SourceManual, got.Source)
		assert.Equal(t, ViaSelf, got.Via)
	})

	t.Run("namespace rule surfaces at namespace level", func(t *testing.T) {
		f := fakeDeps{
			activeNs: active,
			rules:    []OwnershipRuleRow{rule(MatchScopeNamespace, "", "payments", "acct1", OwnerTypeGroup, "ns-rule")},
		}
		got := resolveOne(f.deps(), ResourceTypeNamespace, nsKey)
		assert.True(t, got.Found)
		assert.Equal(t, "ns-rule", got.OwnerId)
		assert.Equal(t, SourceRule, got.Source)
		assert.Equal(t, ViaSelf, got.Via)
	})

	t.Run("manual owner beats namespace rule", func(t *testing.T) {
		f := fakeDeps{
			activeNs: active,
			owners:   map[string]*ResourceOwnerRow{ownerKey(ResourceTypeNamespace, nsKey): ownerRow(OwnerTypeUser, "manual")},
			rules:    []OwnershipRuleRow{rule(MatchScopeNamespace, "", "payments", "acct1", OwnerTypeUser, "rule")},
		}
		got := resolveOne(f.deps(), ResourceTypeNamespace, nsKey)
		assert.Equal(t, "manual", got.OwnerId)
		assert.Equal(t, SourceManual, got.Source)
	})

	t.Run("inherited from cloud account owner", func(t *testing.T) {
		f := fakeDeps{
			activeNs: active,
			owners:   map[string]*ResourceOwnerRow{ownerKey(ResourceTypeCloudAccount, "acct1"): ownerRow(OwnerTypeUser, "cluster")},
		}
		got := resolveOne(f.deps(), ResourceTypeNamespace, nsKey)
		assert.True(t, got.Found)
		assert.Equal(t, "cluster", got.OwnerId)
		assert.Equal(t, ViaCluster, got.Via)
	})
}

func TestResolveOne_CloudAccountAndService(t *testing.T) {
	t.Run("cloud account manual owner", func(t *testing.T) {
		f := fakeDeps{owners: map[string]*ResourceOwnerRow{ownerKey(ResourceTypeCloudAccount, "acct1"): ownerRow(OwnerTypeGroup, "g1")}}
		got := resolveOne(f.deps(), ResourceTypeCloudAccount, "acct1")
		assert.True(t, got.Found)
		assert.Equal(t, "g1", got.OwnerId)
		assert.Equal(t, SourceManual, got.Source)
		assert.Equal(t, ViaSelf, got.Via)
	})

	t.Run("cloud account with no owner", func(t *testing.T) {
		got := resolveOne(fakeDeps{}.deps(), ResourceTypeCloudAccount, "acct1")
		assert.False(t, got.Found)
	})

	t.Run("service manual owner", func(t *testing.T) {
		f := fakeDeps{owners: map[string]*ResourceOwnerRow{ownerKey(ResourceTypeService, "svc-key"): ownerRow(OwnerTypeUser, "u1")}}
		got := resolveOne(f.deps(), ResourceTypeService, "svc-key")
		assert.True(t, got.Found)
		assert.Equal(t, "u1", got.OwnerId)
		assert.Equal(t, ViaSelf, got.Via)
	})

	t.Run("result echoes the requested resource", func(t *testing.T) {
		got := resolveOne(fakeDeps{}.deps(), ResourceTypeCloudAccount, "acct1")
		assert.Equal(t, ResourceTypeCloudAccount, got.ResourceType)
		assert.Equal(t, "acct1", got.ResourceKey)
	})
}

func TestNamespaceKeyParsing(t *testing.T) {
	tests := []struct {
		key         string
		wantAccount string
		wantNs      string
	}{
		{"acct1/payments", "acct1", "payments"},
		{"acct1/team/sub", "acct1", "team/sub"},
		{"noslash", "", ""},
		{"acct1/", "acct1", ""},
		{"/payments", "", ""},
		{"", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			assert.Equal(t, tt.wantAccount, accountFromNamespaceKey(tt.key))
			assert.Equal(t, tt.wantNs, namespaceNameFromKey(tt.key))
		})
	}
}

func activeEc2() *cloudResourceMeta {
	return &cloudResourceMeta{Account: "acct1", Region: "us-east-1", Type: "ec2_instance", ServiceName: "AmazonEC2", Tags: map[string]string{"team": "payments"}}
}

func TestResolveCloudResource(t *testing.T) {
	const id = "r-1"

	t.Run("gone or inactive resource yields no owner (orphan guard)", func(t *testing.T) {
		got := resolveOne(fakeDeps{}.deps(), ResourceTypeCloudResource, id)
		assert.False(t, got.Found)
	})

	t.Run("direct manual owner", func(t *testing.T) {
		f := fakeDeps{
			cloudRes: map[string]*cloudResourceMeta{id: activeEc2()},
			owners:   map[string]*ResourceOwnerRow{ownerKey(ResourceTypeCloudResource, id): ownerRow(OwnerTypeUser, "u1")},
		}
		got := resolveOne(f.deps(), ResourceTypeCloudResource, id)
		assert.True(t, got.Found)
		assert.Equal(t, "u1", got.OwnerId)
		assert.Equal(t, SourceManual, got.Source)
		assert.Equal(t, ViaSelf, got.Via)
	})

	t.Run("cloud rule when no manual owner", func(t *testing.T) {
		f := fakeDeps{
			cloudRes:   map[string]*cloudResourceMeta{id: activeEc2()},
			cloudRules: []OwnershipRuleRow{rule(MatchScopeCloudTag, "team", "payments", "", OwnerTypeGroup, "g1")},
		}
		got := resolveOne(f.deps(), ResourceTypeCloudResource, id)
		assert.True(t, got.Found)
		assert.Equal(t, "g1", got.OwnerId)
		assert.Equal(t, SourceRule, got.Source)
		assert.Equal(t, ViaSelf, got.Via)
	})

	t.Run("manual beats rule", func(t *testing.T) {
		f := fakeDeps{
			cloudRes:   map[string]*cloudResourceMeta{id: activeEc2()},
			owners:     map[string]*ResourceOwnerRow{ownerKey(ResourceTypeCloudResource, id): ownerRow(OwnerTypeUser, "manual")},
			cloudRules: []OwnershipRuleRow{rule(MatchScopeCloudTag, "team", "payments", "", OwnerTypeUser, "rule")},
		}
		got := resolveOne(f.deps(), ResourceTypeCloudResource, id)
		assert.Equal(t, "manual", got.OwnerId)
		assert.Equal(t, SourceManual, got.Source)
	})

	t.Run("inherited from cloud account owner", func(t *testing.T) {
		f := fakeDeps{
			cloudRes: map[string]*cloudResourceMeta{id: activeEc2()},
			owners:   map[string]*ResourceOwnerRow{ownerKey(ResourceTypeCloudAccount, "acct1"): ownerRow(OwnerTypeUser, "cluster")},
		}
		got := resolveOne(f.deps(), ResourceTypeCloudResource, id)
		assert.True(t, got.Found)
		assert.Equal(t, "cluster", got.OwnerId)
		assert.Equal(t, ViaCluster, got.Via)
	})

	t.Run("no owner at any level", func(t *testing.T) {
		f := fakeDeps{cloudRes: map[string]*cloudResourceMeta{id: activeEc2()}}
		got := resolveOne(f.deps(), ResourceTypeCloudResource, id)
		assert.False(t, got.Found)
	})
}

// NoActiveResource is what lets a caller tell "an owner assigned here would stay
// hidden" from "nobody owns this yet". Both are found=false, and a UI that could not
// tell them apart offered an assign that saved and changed nothing.
func TestResolveOne_NoActiveResource(t *testing.T) {
	live := fakeDeps{
		workloads: map[string]*workloadMeta{"wl1": activeApiWorkload()},
		cloudRes:  map[string]*cloudResourceMeta{"cr1": {Account: "acct1", Type: "ec2"}},
		activeNs:  map[string]bool{"acct1/payments": true},
	}

	t.Run("flagged when nothing active has the key", func(t *testing.T) {
		for _, c := range []struct{ resType, key string }{
			{ResourceTypeWorkload, "gone"},
			{ResourceTypeCloudResource, "gone"},
			{ResourceTypeNamespace, "acct1/gone"},
		} {
			got := resolveOne(live.deps(), c.resType, c.key)
			assert.False(t, got.Found, c.resType)
			assert.True(t, got.NoActiveResource, c.resType)
		}
	})

	t.Run("not flagged for an active resource nobody owns", func(t *testing.T) {
		for _, c := range []struct{ resType, key string }{
			{ResourceTypeWorkload, "wl1"},
			{ResourceTypeCloudResource, "cr1"},
			{ResourceTypeNamespace, "acct1/payments"},
		} {
			got := resolveOne(live.deps(), c.resType, c.key)
			assert.False(t, got.Found, c.resType)
			assert.False(t, got.NoActiveResource, c.resType)
		}
	})

	t.Run("flagged by type: a live cloud resource is not an active workload", func(t *testing.T) {
		// An event on a k8s account can carry the id of a node, which is a cloud
		// resource row and not a workload. Asked for as a workload it has no owner to
		// find, and an owner assigned to it as a workload would never show.
		got := resolveOne(live.deps(), ResourceTypeWorkload, "cr1")
		assert.True(t, got.NoActiveResource)
	})

	t.Run("an owner stored on a gone resource stays hidden, and is flagged", func(t *testing.T) {
		f := fakeDeps{owners: map[string]*ResourceOwnerRow{ownerKey(ResourceTypeWorkload, "gone"): ownerRow(OwnerTypeUser, "u1")}}
		got := resolveOne(f.deps(), ResourceTypeWorkload, "gone")
		assert.False(t, got.Found)
		assert.True(t, got.NoActiveResource)
	})

	t.Run("never flagged for types with no existence check", func(t *testing.T) {
		assert.False(t, resolveOne(fakeDeps{}.deps(), ResourceTypeCloudAccount, "acct1").NoActiveResource)
		assert.False(t, resolveOne(fakeDeps{}.deps(), ResourceTypeService, "svc1").NoActiveResource)
	})
}

// A caller who may not assign owners is told only what it was told before.
func TestHideResourceState(t *testing.T) {
	results := []OwnerResult{
		{ResourceType: ResourceTypeWorkload, ResourceKey: "gone", NoActiveResource: true},
		{ResourceType: ResourceTypeWorkload, ResourceKey: "wl1", Found: true, OwnerType: OwnerTypeUser, OwnerId: "u1"},
	}
	hideResourceState(results)
	assert.False(t, results[0].NoActiveResource)
	assert.Equal(t, OwnerResult{ResourceType: ResourceTypeWorkload, ResourceKey: "wl1", Found: true, OwnerType: OwnerTypeUser, OwnerId: "u1"}, results[1])
}

// Reads need only a tenant, so "is this key a live resource" must not reach a caller
// who could not act on it: that would let any user probe for resources in accounts
// they cannot see. A tenant admin context needs a database to build, so what is
// pinned here is the direction that matters — who is NOT told.
func TestCanSeeResourceState_OnlyThoseWhoMayAssign(t *testing.T) {
	asCaller := func(sc *security.SecurityContext) *security.RequestContext {
		return security.NewRequestContext(context.Background(), sc, nil, nil, nil)
	}

	t.Run("a tenant user with no admin role is not told", func(t *testing.T) {
		assert.False(t, canSeeResourceState(asCaller(security.NewSecurityContextForAuditRelay("tenant-1", "user-1"))))
	})

	t.Run("a super admin outside any tenant is not told", func(t *testing.T) {
		assert.False(t, canSeeResourceState(asCaller(security.NewSecurityContextForSuperAdmin())))
	})
}
