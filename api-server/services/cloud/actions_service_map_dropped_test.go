package cloud

import (
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"nudgebee/services/eventrule/playbooks"
)

// The gate itself: an AWS alarm and an Azure alert that both used to qualify
// must no longer schedule the action. Existing deployments keep their
// agent_playbook_action row - the template upsert never deletes - so this, not
// the template, is what stops the card rendering where it already renders.
func TestCloudServiceMapNeverAutoExecutes(t *testing.T) {
	action := cloudServiceMapAction{}
	ctxWith := func(source string, labels map[string]string) playbooks.PlaybookActionContext {
		return playbooks.NewPlaybookActionContext("t", "a", slog.Default(),
			playbooks.PlaybookEvent{Source: source, Labels: labels})
	}

	if action.CanAutoExecute(ctxWith("AWS_Cloudwatch_Alarm", map[string]string{
		"aws_region":         "us-east-1",
		"aws_event_instance": "i-0dcee3621b8456783",
		"aws_service_name":   "AmazonEC2",
	})) {
		t.Error("AWS alarm still schedules cloud_service_map; its downstreams are the " +
			"instance's AMI, VPC, subnet and security group, which contradicts the " +
			"knowledge-graph card on the same event")
	}

	if action.CanAutoExecute(ctxWith("Azure_Monitor_Alert", map[string]string{
		"azure_alert_target_resource": "/subscriptions/00000000-0000-0000-0000-000000000000" +
			"/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm1",
	})) {
		t.Error("Azure alert still schedules cloud_service_map; it returns the same " +
			"containment shape as AWS, so a per-provider carve-out would leave the " +
			"contradiction in place here")
	}
}

// The UI card this action fed is NOT being removed, and must not be: the
// service_map format has another producer, traces_dependency_map, whose links
// are real call edges rather than containment. Deleting the renderer along with
// this action would silently drop trace-derived topology from every event that
// has it.
//
// The template still lists cloud_service_map. Removing that entry would only
// affect fresh installs - the upsert that applies this file never deletes rows,
// so every existing deployment keeps its own - and the entry is inert now that
// the action refuses to auto-execute. Left in place rather than rewritten,
// since re-serializing the file reformats all 78 entries.
func TestServiceMapCardStillHasARealProducer(t *testing.T) {
	raw, err := os.ReadFile("../eventrule/event_actions_template.json")
	if err != nil {
		t.Skipf("template not readable from here: %v", err)
	}
	var entries []map[string]interface{}
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("parsing event action template: %v", err)
	}
	for _, e := range entries {
		if e["action_name"] == "traces_dependency_map" {
			return
		}
	}
	t.Error("traces_dependency_map is gone; the service_map card now has no producer " +
		"and removing the renderer would lose real trace-derived dependencies")
}
