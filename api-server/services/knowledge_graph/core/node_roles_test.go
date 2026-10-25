package core

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestApplyUserRole_UserWinsAndDetectedIsKept(t *testing.T) {
	// A redis workload the classifier read as a database.
	props := map[string]interface{}{"name": "redis-master", PropRole: "database", PropEngine: "postgres"}
	props, qa := ApplyUserRole(props, nil, NodeRoleCache, "redis")

	want := map[string]string{
		PropRole: "cache", PropEngine: "redis", PropRoleSource: RoleSourceUser,
		PropDetectedRole: "database", PropDetectedEngine: "postgres",
	}
	for k, v := range want {
		if got := getNodeProp(props, k); got != v {
			t.Errorf("props[%s] = %q, want %q", k, got, v)
		}
	}
	if qa[PropRole] != "cache" || qa[PropEngine] != "redis" {
		t.Errorf("query_attributes not mirrored: %v", qa)
	}
}

func TestApplyUserRole_ReapplyKeepsFirstDetectedValue(t *testing.T) {
	// A resource patch carries the stored keys forward, so a second apply sees
	// the user's own role in "role"; it must not become the detected value.
	props, _ := ApplyUserRole(map[string]interface{}{PropRole: "database"}, nil, NodeRoleCache, "")
	props, _ = ApplyUserRole(props, nil, NodeRoleSearch, "")
	if got := getNodeProp(props, PropDetectedRole); got != "database" {
		t.Errorf("detected_role = %q, want database", got)
	}
	if got := getNodeProp(props, PropRole); got != "search" {
		t.Errorf("role = %q, want search", got)
	}
}

func TestApplyUserRole_EmptyEngineFallsBackToDetected(t *testing.T) {
	props, qa := ApplyUserRole(map[string]interface{}{PropEngine: "postgres"}, map[string]interface{}{}, NodeRoleDatabase, "")
	if getNodeProp(props, PropEngine) != "postgres" || qa[PropEngine] != "postgres" {
		t.Errorf("engine should fall back to the detected one: props=%v qa=%v", props, qa)
	}
	props, qa = ApplyUserRole(map[string]interface{}{}, map[string]interface{}{PropEngine: "stale"}, NodeRoleProxy, "")
	if _, ok := props[PropEngine]; ok {
		t.Errorf("no engine anywhere: props should have none, got %v", props)
	}
	if _, ok := qa[PropEngine]; ok {
		t.Errorf("no engine anywhere: query_attributes should have none, got %v", qa)
	}
}

func TestClearUserRole_RestoresDetectedAndIsNoOpOtherwise(t *testing.T) {
	props, qa := ApplyUserRole(map[string]interface{}{PropRole: "database", PropEngine: "postgres"}, nil, NodeRoleCache, "redis")
	props, qa = ClearUserRole(props, qa)
	if getNodeProp(props, PropRole) != "database" || getNodeProp(props, PropEngine) != "postgres" {
		t.Errorf("detected role not restored: %v", props)
	}
	for _, k := range []string{PropRoleSource, PropDetectedRole, PropDetectedEngine} {
		if _, ok := props[k]; ok {
			t.Errorf("%s left behind: %v", k, props)
		}
	}
	if qa[PropRole] != "database" {
		t.Errorf("query_attributes not restored: %v", qa)
	}

	// A classifier-only role is not the user's to clear.
	auto := map[string]interface{}{PropRole: "cache"}
	auto, _ = ClearUserRole(auto, nil)
	if getNodeProp(auto, PropRole) != "cache" {
		t.Errorf("classifier role must survive a clear: %v", auto)
	}
}

func TestApplyUserRoles_AppliesRowsAndStripsStaleUserRoles(t *testing.T) {
	tagged := &DbNode{ID: "a", Properties: map[string]interface{}{"name": "pg"}}
	stale, _ := ApplyUserRole(map[string]interface{}{PropRole: "database"}, nil, NodeRoleCache, "")
	removed := &DbNode{ID: "b", Properties: stale}
	plain := &DbNode{ID: "c", Properties: map[string]interface{}{PropRole: "messagequeue"}}

	ApplyUserRoles([]*DbNode{tagged, removed, plain, nil}, map[string]NodeRole{"a": {Role: NodeRoleDatabase, Engine: "postgres"}})

	if getNodeProp(tagged.Properties, PropRole) != "database" || tagged.QueryAttributes[PropRole] != "database" {
		t.Errorf("row not applied: %v %v", tagged.Properties, tagged.QueryAttributes)
	}
	if getNodeProp(removed.Properties, PropRole) != "database" || getNodeProp(removed.Properties, PropRoleSource) != "" {
		t.Errorf("a node whose row is gone must lose the user role: %v", removed.Properties)
	}
	if getNodeProp(plain.Properties, PropRole) != "messagequeue" {
		t.Errorf("a classifier role must be left alone: %v", plain.Properties)
	}
}

func TestEffectiveRole(t *testing.T) {
	cases := []struct {
		nt    NodeType
		props map[string]interface{}
		want  string
	}{
		{NodeTypeDatabase, nil, NodeRoleDatabase},
		{NodeTypeTopic, nil, NodeRoleMessageQueue},
		{NodeTypeWorkload, map[string]interface{}{PropRole: "cache"}, NodeRoleCache},
		{NodeTypeDatabase, map[string]interface{}{PropRole: "search"}, NodeRoleSearch},
		{NodeTypeStorage, nil, ""}, // persistent disks are Storage too: not object storage by type
		{NodeTypeWorkload, nil, ""},
		{NodeTypeCloudResource, map[string]interface{}{PropRole: "core-switch"}, ""}, // a custom kind's free role
	}
	for _, c := range cases {
		if got := EffectiveRole(c.nt, c.props); got != c.want {
			t.Errorf("EffectiveRole(%s, %v) = %q, want %q", c.nt, c.props, got, c.want)
		}
	}
}

func TestNodeRoleVocabularyAndTargets(t *testing.T) {
	for _, r := range NodeRoles {
		if !IsKnownNodeRole(r.Value) || r.Label == "" {
			t.Errorf("bad vocabulary entry %+v", r)
		}
	}
	for role := range NodeRoleTypeEquivalents {
		if !IsKnownNodeRole(role) {
			t.Errorf("type equivalent for unknown role %q", role)
		}
	}
	if IsKnownNodeRole("db") || IsKnownNodeRole("") {
		t.Error("only vocabulary values are roles")
	}
	for _, nt := range []NodeType{NodeTypeWorkload, NodeTypeComputeInstance, NodeTypeK8sService, NodeTypeCloudResource, NodeTypeDatabase} {
		if !IsNodeRoleTarget(nt) {
			t.Errorf("%s should accept a role", nt)
		}
	}
	for _, nt := range []NodeType{NodeTypeNamespace, NodeTypeCluster, NodeTypePod, NodeTypeNode, NodeTypeSubnet, NodeTypeVPC, NodeTypeContainerImage, NodeTypeUserAccount} {
		if IsNodeRoleTarget(nt) {
			t.Errorf("%s must not accept a role", nt)
		}
	}
	for _, nt := range ManualNodeTypes {
		if !IsNodeRoleTarget(nt) {
			t.Errorf("declarable type %s (custom kinds sit under these) must accept a role", nt)
		}
	}
}

func TestMaterializeManualNode_DropsRoleBookkeepingKeys(t *testing.T) {
	// A declaration stored before these keys were refused must still build,
	// and must not read as a user-set role. A free "role" stays.
	node, err := MaterializeManualNode(NodeTypeCloudResource, "NetworkSwitch", "external:t::CloudResource::sw1",
		map[string]interface{}{"name": "sw1"},
		ManualNodeDetails{Properties: map[string]interface{}{PropRole: "core-switch", PropRoleSource: RoleSourceUser, PropDetectedRole: "x"}},
		"t", "t")
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if _, ok := node.Properties[PropRoleSource]; ok {
		t.Errorf("role_source must be dropped: %v", node.Properties)
	}
	if _, ok := node.Properties[PropDetectedRole]; ok {
		t.Errorf("detected_role must be dropped: %v", node.Properties)
	}
	if getNodeProp(node.Properties, PropRole) != "core-switch" {
		t.Errorf("a free role property must survive: %v", node.Properties)
	}
}

func TestNormalizeEngine(t *testing.T) {
	for in, want := range map[string]string{"": "", " PostgreSQL ": "postgresql", "aurora-mysql": "aurora-mysql", "redis7.2": "redis7.2"} {
		got, err := NormalizeEngine(in)
		if err != nil || got != want {
			t.Errorf("NormalizeEngine(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"my sql", "<script>", strings.Repeat("a", 65)} {
		if _, err := NormalizeEngine(bad); err == nil {
			t.Errorf("NormalizeEngine(%q) should fail", bad)
		}
	}
}

func TestNodeTypesForRoles(t *testing.T) {
	got := NodeTypesForRoles([]string{NodeRoleMessageQueue, NodeRoleProxy, NodeRoleMessageQueue})
	if len(got) != 3 {
		t.Errorf("want MessageQueue, Queue, Topic once each, got %v", got)
	}
}

// TestEveryNodeWriterAppliesUserRoles fails when a file writes node
// properties to knowledge_graph_node without layering user roles on. Every
// such writer would otherwise strip the roles users set, silently, on its
// next run. A new writer must call ApplyStoredUserRoles (or ApplyUserRoles /
// ApplyUserRole / ClearUserRole) before it writes.
func TestEveryNodeWriterAppliesUserRoles(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	writes := []*regexp.Regexp{
		regexp.MustCompile(`Insert\([^\n]*"knowledge_graph_node"`),
		regexp.MustCompile(`(?i)INSERT\s+INTO\s+knowledge_graph_node\b`),
		regexp.MustCompile(`(?is)UPDATE\s+knowledge_graph_node\s+SET[^;` + "`" + `]*\bproperties\s*=`),
	}
	applies := regexp.MustCompile(`\b(ApplyStoredUserRoles|ApplyUserRoles|ApplyUserRole|ClearUserRole)\(`)

	var writers []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, w := range writes {
			if w.Match(src) {
				rel, _ := filepath.Rel(root, path)
				writers = append(writers, rel)
				if !applies.Match(src) {
					t.Errorf("%s writes knowledge_graph_node properties without applying user node roles; call core.ApplyStoredUserRoles before the write", rel)
				}
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Guard the guard: if the patterns stop matching the known writer, the
	// test passes vacuously.
	found := false
	for _, w := range writers {
		if strings.HasSuffix(w, filepath.Join("knowledge_graph", "core", "service.go")) {
			found = true
		}
	}
	if !found {
		t.Errorf("forcing test no longer sees core.SaveNodes as a writer (found %v); fix the patterns", writers)
	}
}
