package triage

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"nudgebee/services/internal/database/models"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// incident_group_app.go relates cloud alarms through the tags on the resources
// they are about, for the alarms the service map cannot place. Two relations:
//
//   - Same application: both resources carry the same application tag (and the
//     same environment tag, when both carry one). Several hosts of one
//     application alarming within minutes of each other are one problem far more
//     often than not — an antivirus scan pinning a farm's CPU, a cluster losing
//     quorum, a broker dropping every desktop at once — and nothing else joins
//     them: they rarely call one another, so the service map has no edge.
//   - Patch wave: both alarms are restarts, and both resources carry the same
//     patch-schedule tag. A scheduled patch run restarts every host of a group
//     within minutes, across applications.
//
// Before this, cross-host cloud groups came from the service map, through the
// node the VPC flow source collects every unidentified address into. That made
// every instance of a VPC two hops from every other: the hosts that happened to
// alarm together were grouped whether or not they had anything in common, and
// most hosts of one application that did were not.
//
// Only alerts that fired inside the attach window relate this way, and an alert
// that fires all the time (noisy) only with one that started within minutes of
// it (noisyResourceJoinWindow) — a pool of hosts with standing disk alarms would
// otherwise hold one incident open for as long as any of them kept firing.
// Measured on two weeks of cloud alarms across eight accounts: no group stayed
// open past three hours, and 29 of 30 sampled same-application links joined
// alarms a person would call one problem.

const (
	// sameApplicationReasonPrefix and patchWaveReasonPrefix start every link these
	// relations write, so their links can be counted, audited, or removed as one
	// set.
	sameApplicationReasonPrefix = "same application:"
	patchWaveReasonPrefix       = "patch wave:"
	// patchGroupTagKey is the AWS Systems Manager patch-group tag, normalised.
	patchGroupTagKey = "patchgroup"
)

// cloudResourceAlarmSources are the sources whose alerts are about the cloud
// resource the event is linked to. Other events can carry a resource too — a
// Kubernetes alert carries its cluster — and reading tags there would relate
// every pod alert in a cluster through the cluster's tags. An Azure alert pushed
// by webhook is linked to its resource as a polled one is, and one firing that
// arrives both ways is one event stored under whichever came last.
var cloudResourceAlarmSources = []string{"AWS_CloudWatch_Alarm", "AWS_EventBridge", "Azure_Monitor_Alert", "azure_monitor_webhook", "GCP_Metric_Alert"}

// applicationTagKeys are the tags that name an application, most specific first,
// normalised (resourceTagKey). Generic organisation-wide tags (project, team,
// cost centre) are left out on purpose: they cover too much to mean "one
// problem".
var applicationTagKeys = []string{"applicationname", "application", "app", "awscloudformationstackname"}

// environmentTagKeys name the environment; an application's dev and prod hosts
// are not one problem.
var environmentTagKeys = []string{"environment", "env"}

// restartAlarm matches the alarms that say a resource restarted.
var restartAlarm = regexp.MustCompile(`(?i)reboot|restart`)

// resourceTags is what the relations read off an alarm's resource, lower-cased.
type resourceTags struct {
	App        string
	Env        string
	PatchGroup string
}

// resourceTagKey normalises a tag key: providers and teams spell the same tag
// "application-name", "Application Name" and "application_name".
func resourceTagKey(k string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(k) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// allResourceTagKeys is every normalised key the relations read, built once.
var allResourceTagKeys = append(append(append([]string{}, applicationTagKeys...), environmentTagKeys...), patchGroupTagKey)

// resourceTagKeys is every normalised key the relations read.
func resourceTagKeys() []string {
	return allResourceTagKeys
}

// resourceTagsSQL selects, for the resource aliased r, the tags the relations
// read: a JSON object of normalised key to lower-cased value. Tag values are
// stored as a list ({"Name": ["web-1"]}) by the cloud collector; a plain string
// is accepted too. %s is the parameter holding resourceTagKeys.
const resourceTagsSQL = `(SELECT jsonb_object_agg(t.k, t.v) FROM (
		SELECT lower(regexp_replace(kv.key, '[^A-Za-z0-9]', '', 'g')) AS k,
		       lower(btrim(CASE jsonb_typeof(kv.value) WHEN 'array' THEN kv.value->>0 ELSE kv.value#>>'{}' END)) AS v
		FROM jsonb_each(CASE WHEN jsonb_typeof(r.tags) = 'object' THEN r.tags ELSE '{}'::jsonb END) kv
	) t WHERE t.k = ANY(%s) AND t.v <> '')`

// parseResourceTags reads resourceTagsSQL's output. Unparseable or empty input
// gives no tags, and so no relation.
func parseResourceTags(raw *string) resourceTags {
	if raw == nil || *raw == "" {
		return resourceTags{}
	}
	var tags map[string]string
	if json.Unmarshal([]byte(*raw), &tags) != nil {
		return resourceTags{}
	}
	first := func(keys []string) string {
		for _, k := range keys {
			if v := tags[k]; v != "" {
				return v
			}
		}
		return ""
	}
	return resourceTags{App: first(applicationTagKeys), Env: first(environmentTagKeys), PatchGroup: tags[patchGroupTagKey]}
}

// isCloudResourceAlarm reports whether an event from this source is about its
// linked cloud resource.
func isCloudResourceAlarm(source string) bool {
	for _, s := range cloudResourceAlarmSources {
		if source == s {
			return true
		}
	}
	return false
}

// loadSeedResourceTags reads the tags of the resource the seed alarm is about.
// Empty when the event is not a cloud resource alarm or is linked to none.
func loadSeedResourceTags(ctx context.Context, db sqlx.ExtContext, event *models.Event) (resourceTags, error) {
	if event.CloudResourceId == nil || *event.CloudResourceId == "" || !isCloudResourceAlarm(deref(event.Source)) {
		return resourceTags{}, nil
	}
	var raw []*string
	if err := sqlx.SelectContext(ctx, db, &raw,
		`SELECT `+fmt.Sprintf(resourceTagsSQL, "$2")+`::text FROM cloud_resourses r WHERE r.id = $1`,
		*event.CloudResourceId, pq.Array(resourceTagKeys())); err != nil {
		return resourceTags{}, fmt.Errorf("failed to load the alarm's resource tags: %w", err)
	}
	if len(raw) == 0 {
		return resourceTags{}, nil
	}
	return parseResourceTags(raw[0]), nil
}

// resourceRelation is how two alarms that started inside the attach window
// relate through their resources' tags, or 0. Pure.
func resourceRelation(seed, m alertMember) relation {
	a, b := seed.Resource, m.Resource
	if a.App != "" && a.App == b.App && (a.Env == "" || b.Env == "" || a.Env == b.Env) {
		return relSameApp
	}
	if a.PatchGroup != "" && a.PatchGroup == b.PatchGroup &&
		restartAlarm.MatchString(seed.AggregationKey) && restartAlarm.MatchString(m.AggregationKey) {
		return relPatchWave
	}
	return 0
}

// resourceReason is the link reason for a tag relation; subject is the alert
// being linked.
func resourceReason(rel relation, tags resourceTags, subject string) string {
	if rel == relPatchWave {
		return fmt.Sprintf("%s patch group %s; %s restarted within the incident attach window", patchWaveReasonPrefix, tags.PatchGroup, subject)
	}
	return fmt.Sprintf("%s %s; %s fired within the incident attach window", sameApplicationReasonPrefix, tags.App, subject)
}

// noisyResourceJoinWindow is how close together two alarms related only through
// tags must have started when either of them fires all the time (noisy).
//
// Dropping noisy alarms from the tag relations altogether was measured first,
// and it threw away the case the relations exist for: an application's database
// and its web app, both alarming several times a day under load, started within
// a minute of each other on most occasions and were never joined. Letting them
// join anywhere in the attach window is what a standing alarm on one host of a
// large pool would use to meet every other alarm in the pool. Starting within
// five minutes keeps the first and, over two weeks of cloud alarms across eight
// accounts, gave no group open past three hours.
const noisyResourceJoinWindow = 5 * time.Minute

// dropNoisyResourceMembers removes the alerts related only through tags when
// the alert or the seed fires all the time and the two did not start within
// noisyResourceJoinWindow of each other. Same-subject and service-map relations
// keep their noisy members whatever their start: there a noisy alert only
// loses the leader election, while a tag relation has nothing but the clock
// and the tag to go on.
func dropNoisyResourceMembers(related []relatedMember, noisy map[string]bool, seed alertMember) []relatedMember {
	out := related[:0]
	for _, m := range related {
		gap := m.StartsAt.Sub(seed.StartsAt)
		if gap < 0 {
			gap = -gap
		}
		if (m.Relation == relSameApp || m.Relation == relPatchWave) &&
			(noisy[m.Fingerprint] || noisy[seed.Fingerprint]) && gap > noisyResourceJoinWindow {
			continue
		}
		out = append(out, m)
	}
	return out
}
