package tools

import (
	"testing"

	toolcore "nudgebee/llm/tools/core"
)

// unconfirmedTools are registered tools whose reach could not be confirmed from source.
// They are deliberately left out of the scope table (they resolve to unclassified, i.e.
// treated as a gap) and are listed here so that a NEW registered tool without a scope
// fails this test instead of silently taking the default.
var unconfirmedTools = map[string]bool{
	"recommendation_apply":       true,
	"standard_diagnostic_grep":   true,
	"ticket_master_v2":           true,
	"workflow_config_save":       true,
	"workflow_make_version_live": true,
	"workflow_publish":           true,
	"workflow_restore_version":   true,
	"workflow_validate":          true,
}

func TestEveryRegisteredToolHasAScopeOrIsKnownUnconfirmed(t *testing.T) {
	for _, name := range toolcore.ListRegisteredSystemToolNames() {
		_, classified := toolcore.ClassifyCapability(name)
		switch {
		case classified && unconfirmedTools[name]:
			t.Errorf("%q is now classified; remove it from unconfirmedTools", name)
		case !classified && !unconfirmedTools[name]:
			t.Errorf("registered tool %q has no entry in capabilityScopeTable: read its InputSchema and classify it "+
				"(or, if its reach cannot be confirmed, add it to unconfirmedTools)", name)
		}
	}
}

func TestUnconfirmedToolsAreStillRegistered(t *testing.T) {
	registered := map[string]bool{}
	for _, name := range toolcore.ListRegisteredSystemToolNames() {
		registered[name] = true
	}
	for name := range unconfirmedTools {
		if !registered[name] {
			t.Errorf("%q is in unconfirmedTools but no longer registered; remove it", name)
		}
	}
}
