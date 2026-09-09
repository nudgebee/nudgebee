package providers

import (
	"testing"
	"time"
)

// One CloudWatch state change reaches us twice: EventBridge pushes it, and the
// alarm poller finds it about five minutes later. Both already keyed on the same
// two facts — the alarm ARN and the transition timestamp — but printed them
// differently, so the unique index on (tenant, cloud_account_id, finding_id)
// could not match them:
//
//	arn:...:alarm:order-down-1788602696            push
//	arn:...:alarm:order-down/2026-09-05T10:04:56Z  poll
//
// Measured on dev: every scenario alarm stored twice, and every firing advanced
// its dedup chain by two rather than one.
//
// This is the contract that stops that happening again. It is not a formatting
// test — the two paths agreeing is the entire dedup mechanism, because there is
// no separate reconciliation step downstream.
func TestFiringFindingIDAgreesAcrossIngestionPaths(t *testing.T) {
	const alarmARN = "arn:aws:cloudwatch:us-east-1:864186153326:alarm:nudgebee-scenario-services-order-down"
	transition := time.Date(2026, 9, 5, 10, 4, 56, 0, time.UTC)

	// EventBridge: the rule supplies the alarm ARN as the fingerprint, and the
	// delivery carries the state-change time.
	push := Event{
		EventId:     alarmARN,
		Date:        transition,
		EventSource: "AWS_EventBridge",
	}
	// The poller: same alarm, same StateTransitionedTimestamp, found later.
	poll := Event{
		EventId:     alarmARN,
		Date:        transition,
		EventSource: "AWS_CloudWatch_Alarm",
	}

	if push.FiringFindingID() != poll.FiringFindingID() {
		t.Fatalf("the same alarm firing produced two identities:\n  push %q\n  poll %q\n"+
			"They will be stored as two events, and each firing will advance the dedup "+
			"chain twice.", push.FiringFindingID(), poll.FiringFindingID())
	}

	// The identity has to change when the firing does, or a later firing of the
	// same alarm would be swallowed as a duplicate of the first.
	later := Event{EventId: alarmARN, Date: transition.Add(time.Minute)}
	if later.FiringFindingID() == push.FiringFindingID() {
		t.Error("a different firing of the same alarm produced the same identity; " +
			"the second one would be dropped")
	}

	// Different alarms never collide, whatever the timing.
	other := Event{EventId: alarmARN + "-cpu", Date: transition}
	if other.FiringFindingID() == push.FiringFindingID() {
		t.Error("two different alarms share an identity")
	}
}

// A source that supplies its own per-firing id keeps it: some providers carry a
// genuinely unique delivery id, and rewriting it would lose the dedup they
// already give us.
func TestFiringFindingIDPrefersSourceNativeID(t *testing.T) {
	e := Event{
		FindingId: "provider-native-12345",
		EventId:   "arn:aws:cloudwatch:us-east-1:1:alarm:x",
		Date:      time.Date(2026, 9, 5, 10, 4, 56, 0, time.UTC),
	}
	if got := e.FiringFindingID(); got != "provider-native-12345" {
		t.Errorf("FiringFindingID() = %q, want the source-native id", got)
	}
}

// The identity must not depend on the wall-clock zone of whichever collector
// replica happened to handle the event — the same instant in two locations is
// one firing.
func TestFiringFindingIDIsTimezoneIndependent(t *testing.T) {
	utc := time.Date(2026, 9, 5, 10, 4, 56, 0, time.UTC)
	ist := utc.In(time.FixedZone("IST", 5*3600+1800))

	a := Event{EventId: "arn:alarm:x", Date: utc}
	b := Event{EventId: "arn:alarm:x", Date: ist}
	if a.FiringFindingID() != b.FiringFindingID() {
		t.Errorf("same instant in two zones produced %q and %q",
			a.FiringFindingID(), b.FiringFindingID())
	}
}
