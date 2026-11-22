package security

import (
	_ "embed"
	"encoding/json"
)

// action_keys.json maps every grantable RPC action to the "<module>:<Class>"
// grant key the app gateway classifies it as. It is generated from
// app/src/lib/permissionCatalog.ts and pinned there by actionKeysExport.test.ts;
// do not edit it by hand.
//
//go:embed action_keys.json
var actionKeysJSON []byte

var actionKeys = func() map[string]string {
	keys := map[string]string{}
	if err := json.Unmarshal(actionKeysJSON, &keys); err != nil {
		panic("security: action_keys.json: " + err.Error())
	}
	return keys
}()

// ActionKey is the grant key for an action: the one the caller sent (the
// gateway's classification, or a caller's own, such as llm-server's Execute for
// a change the AI makes), else the gateway's classification of the action by
// name. A caller that skips the gateway (llm-server reads, internal jobs) is
// then checked against the same key the role editor grants. Empty for an action
// that is not grantable, whatever key the caller sent: no grant reaches it.
func ActionKey(actionName, sent string) string {
	key, known := actionKeys[actionName]
	if known && key == "" {
		return ""
	}
	if sent != "" {
		return sent
	}
	return key
}
