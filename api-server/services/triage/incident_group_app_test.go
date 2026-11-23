package triage

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tagsJSON(s string) *string { return &s }

func TestParseResourceTags(t *testing.T) {
	t.Run("most specific application key wins", func(t *testing.T) {
		got := parseResourceTags(tagsJSON(`{"app":"short","applicationname":"billing","environment":"prod","patchgroup":"group-a-sun"}`))
		assert.Equal(t, resourceTags{App: "billing", Env: "prod", PatchGroup: "group-a-sun"}, got)
	})
	t.Run("a stack name stands in when no application tag is set", func(t *testing.T) {
		got := parseResourceTags(tagsJSON(`{"awscloudformationstackname":"orders-stack","env":"dev"}`))
		assert.Equal(t, resourceTags{App: "orders-stack", Env: "dev"}, got)
	})
	t.Run("nothing to read", func(t *testing.T) {
		assert.Equal(t, resourceTags{}, parseResourceTags(nil))
		assert.Equal(t, resourceTags{}, parseResourceTags(tagsJSON("")))
		assert.Equal(t, resourceTags{}, parseResourceTags(tagsJSON("not json")))
	})
}

func TestResourceTagKey(t *testing.T) {
	// The same tag as three providers / teams spell it.
	for _, k := range []string{"application-name", "Application Name", "application_name", "ApplicationName"} {
		assert.Equal(t, "applicationname", resourceTagKey(k), k)
	}
	assert.Equal(t, "patchgroup", resourceTagKey("Patch Group"))
	assert.Equal(t, "awscloudformationstackname", resourceTagKey("aws:cloudformation:stack-name"))
	for _, k := range resourceTagKeys() {
		assert.Equal(t, k, resourceTagKey(k), "every key the SQL filters on is already normalised")
	}
}

func TestResourceRelation(t *testing.T) {
	alarm := func(key string, tags resourceTags) alertMember {
		return alertMember{AggregationKey: key, Resource: tags}
	}
	web := resourceTags{App: "orders", Env: "prod", PatchGroup: "group-a"}

	assert.Equal(t, relSameApp, resourceRelation(alarm("cpu-high", web), alarm("disk-low", resourceTags{App: "orders", Env: "prod"})))
	assert.Equal(t, relSameApp, resourceRelation(alarm("cpu-high", web), alarm("disk-low", resourceTags{App: "orders"})),
		"an environment tag on one side only does not keep them apart")
	assert.Equal(t, relation(0), resourceRelation(alarm("cpu-high", web), alarm("cpu-high", resourceTags{App: "orders", Env: "dev"})),
		"one application's dev and prod hosts are not one problem")
	assert.Equal(t, relation(0), resourceRelation(alarm("cpu-high", web), alarm("cpu-high", resourceTags{App: "billing", Env: "prod"})))

	other := resourceTags{App: "billing", PatchGroup: "group-a"}
	assert.Equal(t, relPatchWave, resourceRelation(alarm("host-1-OSReboot-Alarm", web), alarm("host-2-WindowsOSReboot", other)),
		"two restarts in one patch group are one patch run, whatever the application")
	assert.Equal(t, relPatchWave, resourceRelation(alarm("vm-restarted", web), alarm("instance-restart", other)))
	assert.Equal(t, relation(0), resourceRelation(alarm("host-1-OSReboot-Alarm", web), alarm("host-2-CPUUtilization", other)),
		"a patch group says nothing about an alarm that is not a restart")
	assert.Equal(t, relation(0), resourceRelation(alarm("host-1-OSReboot-Alarm", web), alarm("host-2-OSReboot", resourceTags{PatchGroup: "group-b"})))

	assert.Equal(t, relation(0), resourceRelation(alarm("cpu-high", resourceTags{}), alarm("cpu-high", resourceTags{})),
		"untagged resources never relate")
}

func TestRelateMembers_TagRelations(t *testing.T) {
	now := time.Date(2026, 10, 5, 22, 4, 19, 0, time.UTC)
	seed := alertMember{Fingerprint: "seed", SubjectKey: "amazonec2|web-1", AggregationKey: "web-1-cpu-high",
		StartsAt: now, Recent: true, Resource: resourceTags{App: "orders", Env: "prod", PatchGroup: "group-a"}}
	m := func(fp, subject, key string, tags resourceTags, recent bool) *alertMember {
		return &alertMember{Fingerprint: fp, SubjectKey: "amazonec2|" + subject, AggregationKey: key, StartsAt: now, Recent: recent, Resource: tags}
	}

	related, _ := relateMembers(nil, seed, false, []*alertMember{
		m("same-app", "web-2", "web-2-cpu-high", resourceTags{App: "orders", Env: "prod"}, true),
		m("same-app-earlier-firing-only", "web-3", "web-3-cpu-high", resourceTags{App: "orders"}, false),
		m("other-app", "db-1", "db-1-cpu-high", resourceTags{App: "billing"}, true),
		m("untagged", "web-4", "web-4-cpu-high", resourceTags{}, true),
	})
	got := map[string]relation{}
	for _, r := range related {
		got[r.Fingerprint] = r.Relation
	}
	assert.Equal(t, map[string]relation{"same-app": relSameApp}, got,
		"only an alarm that fired in the window on the seed's application relates, with no service map needed")
}

func TestDropNoisyResourceMembers(t *testing.T) {
	now := time.Date(2026, 10, 5, 22, 4, 19, 0, time.UTC)
	seed := alertMember{Fingerprint: "seed", StartsAt: now}
	member := func(fp string, rel relation, offset time.Duration) relatedMember {
		return relatedMember{alertMember: alertMember{Fingerprint: fp, StartsAt: now.Add(offset)}, Relation: rel}
	}
	related := []relatedMember{
		member("quiet-late", relSameApp, 12*time.Minute),
		member("noisy-together", relSameApp, -2*time.Minute),
		member("noisy-late", relSameApp, 12*time.Minute),
		member("noisy-restart-late", relPatchWave, -9*time.Minute),
		member("noisy-neighbour", relConnected, 12*time.Minute),
	}
	noisy := map[string]bool{"noisy-together": true, "noisy-late": true, "noisy-restart-late": true, "noisy-neighbour": true}

	kept := func(rs []relatedMember) []string {
		var out []string
		for _, r := range rs {
			out = append(out, r.Fingerprint)
		}
		return out
	}
	assert.Equal(t, []string{"quiet-late", "noisy-together", "noisy-neighbour"},
		kept(dropNoisyResourceMembers(append([]relatedMember{}, related...), noisy, seed)),
		"a noisy alarm joins through a tag only when it started within minutes of the seed; other relations keep it")

	noisy["seed"] = true
	assert.Equal(t, []string{"noisy-together", "noisy-neighbour"},
		kept(dropNoisyResourceMembers(append([]relatedMember{}, related...), noisy, seed)),
		"a noisy seed is held to the same window")
}

func TestTagRelationReasons(t *testing.T) {
	in := decideInput{SeedKey: "amazonec2|web-1"}
	reason, dist := in.linkReason(relatedMember{Relation: relSameApp, alertMember: alertMember{Resource: resourceTags{App: "orders"}}})
	assert.Zero(t, dist)
	assert.True(t, strings.HasPrefix(reason, sameApplicationReasonPrefix), reason)
	assert.Contains(t, reason, "orders")

	reason, _ = in.linkReason(relatedMember{Relation: relPatchWave, alertMember: alertMember{Resource: resourceTags{PatchGroup: "group-a"}}})
	assert.True(t, strings.HasPrefix(reason, patchWaveReasonPrefix), reason)
	assert.Contains(t, reason, "group-a")

	// A member brought along is linked with the same prefix, naming itself.
	d := decideGroup(decideInput{
		Seed:    alertMember{Fingerprint: "seed", RepRow: "seed-row", StartsAt: time.Unix(100, 0)},
		SeedKey: "amazonec2|web-1",
		Related: []relatedMember{{
			alertMember: alertMember{Fingerprint: "m", RepRow: "m-row", Rows: []string{"m-row"}, SubjectKey: "amazonec2|web-2",
				StartsAt: time.Unix(200, 0), Recent: true, Resource: resourceTags{App: "orders"}},
			Relation: relSameApp,
		}},
	})
	require.Len(t, d.Bring, 1)
	assert.True(t, strings.HasPrefix(d.Bring[0].Reason, sameApplicationReasonPrefix), d.Bring[0].Reason)
	assert.Contains(t, d.Bring[0].Reason, "amazonec2|web-2")
}
