package event

import (
	"testing"

	"nudgebee/services/internal/database/models"

	"github.com/stretchr/testify/assert"
)

// Restart used to delete a single pod whatever owned it. On a Deployment that restarts one replica
// and leaves the rest on the old state, which is not what "Restart" means to an operator and often
// does not clear the condition at all.
func TestRestartActionFor(t *testing.T) {
	ns := "shop"
	event := func(ownerKind, owner, subject string) (models.Event, *string) {
		e := models.Event{SubjectNamespace: &ns}
		subjectPtr := &subject
		e.SubjectName = subjectPtr
		if ownerKind != "" {
			e.SubjectOwnerKind = &ownerKind
		}
		if owner != "" {
			e.SubjectOwner = &owner
		}
		return e, subjectPtr
	}

	t.Run("controller-owned pods roll the whole workload", func(t *testing.T) {
		for _, kind := range []string{"Deployment", "StatefulSet", "DaemonSet", "Rollout"} {
			ev, _ := event(kind, "services-server", "services-server-abc-123")
			action, params := restartActionFor(ev)
			assert.Equal(t, "rollout_restart", action, kind)
			assert.Equal(t, kind, params["kind"])
			// The controller is the target, never the individual pod.
			assert.Equal(t, "services-server", params["name"], kind)
			assert.Equal(t, ns, params["namespace"])
		}
	})

	// subject_owner_kind is not written consistently — cloud_resourses alone holds both "Pod" and
	// "pod" — so kind matching cannot be case-sensitive.
	t.Run("kind casing does not change the outcome", func(t *testing.T) {
		ev, _ := event("deployment", "services-server", "services-server-abc-123")
		action, params := restartActionFor(ev)
		assert.Equal(t, "rollout_restart", action)
		assert.Equal(t, "deployment", params["kind"])
	})

	// A standalone pod has no controller to roll; recreating it IS the restart.
	t.Run("a bare pod is still deleted", func(t *testing.T) {
		ev, _ := event("", "", "standalone-pod")
		action, params := restartActionFor(ev)
		assert.Equal(t, "delete_pod", action)
		assert.Equal(t, "standalone-pod", params["name"])
		assert.Equal(t, false, params["previous"])
	})

	t.Run("an owner kind the agent cannot roll falls back to the pod", func(t *testing.T) {
		ev, _ := event("Job", "nightly-import", "nightly-import-abc")
		action, _ := restartActionFor(ev)
		assert.Equal(t, "delete_pod", action)
	})

	// Owner kind without an owner name would produce a rollout_restart with no target.
	t.Run("owner kind without a name falls back to the pod", func(t *testing.T) {
		ev, _ := event("Deployment", "", "services-server-abc-123")
		action, params := restartActionFor(ev)
		assert.Equal(t, "delete_pod", action)
		assert.Equal(t, "services-server-abc-123", params["name"])
	})
}
