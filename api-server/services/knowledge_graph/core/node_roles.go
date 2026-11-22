package core

import (
	"fmt"
	"regexp"
	"strings"
)

// Node roles: what a node acts as (a database, a cache, a queue, ...), set by
// a user on a workload, a VM or a declared node. A node's type says what it is
// (a Kubernetes Workload, a ComputeInstance); its role says what it does.
//
// The role lives in the kg_node_roles table, keyed by node id, and is layered
// onto the node every time the node is written (ApplyUserRoles, called from
// SaveNodes and every other writer of knowledge_graph_node). On the node it
// uses the keys every reader already understands, properties.role and
// properties.engine, which the Kubernetes classifier (sources/k8s/db_classifier.go)
// also writes. A user role wins: the classifier's value is kept aside in
// detected_role / detected_engine, so the UI can show what was detected and
// removing the user role restores it at once.

// The roles a node may act as. Values are stored and compared as written.
const (
	NodeRoleDatabase      = "database"
	NodeRoleCache         = "cache"
	NodeRoleMessageQueue  = "messagequeue"
	NodeRoleSearch        = "search"
	NodeRoleObjectStorage = "objectstorage"
	NodeRoleLoadBalancer  = "loadbalancer"
	NodeRoleProxy         = "proxy"
	NodeRoleAPIGateway    = "apigateway"
	NodeRoleScheduler     = "scheduler"
	NodeRoleMonitoring    = "monitoring"
	NodeRoleAuth          = "auth"
)

// Property keys the role uses on a node.
const (
	PropRole           = "role"
	PropEngine         = "engine"
	PropRoleSource     = "role_source"
	PropDetectedRole   = "detected_role"
	PropDetectedEngine = "detected_engine"

	// RoleSourceUser marks a role a user set. A node without role_source
	// carries the classifier's role, or none.
	RoleSourceUser = "user"
)

// IsNodeRoleBookkeepingProperty reports whether key is one the role overlay
// owns on a node (role_source, detected_role, detected_engine). A user may not
// set them as properties: role_source="user" with no role row would read as a
// user-set role. role and engine themselves stay ordinary properties: a custom
// kind may describe itself with a free "role" (a switch's "core-switch"), and a
// declared database names its "engine".
func IsNodeRoleBookkeepingProperty(key string) bool {
	return key == PropRoleSource || key == PropDetectedRole || key == PropDetectedEngine
}

// NodeRoleDef is one entry of the vocabulary, as the UI lists it.
type NodeRoleDef struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// NodeRoles is the closed vocabulary, in the order the UI offers it.
var NodeRoles = []NodeRoleDef{
	{NodeRoleDatabase, "Database"},
	{NodeRoleCache, "Cache"},
	{NodeRoleMessageQueue, "Message queue"},
	{NodeRoleSearch, "Search"},
	{NodeRoleObjectStorage, "Object storage"},
	{NodeRoleLoadBalancer, "Load balancer"},
	{NodeRoleProxy, "Proxy"},
	{NodeRoleAPIGateway, "API gateway"},
	{NodeRoleScheduler, "Scheduler"},
	{NodeRoleMonitoring, "Monitoring"},
	{NodeRoleAuth, "Auth"},
}

// IsKnownNodeRole reports whether role is in the vocabulary.
func IsKnownNodeRole(role string) bool {
	for _, r := range NodeRoles {
		if r.Value == role {
			return true
		}
	}
	return false
}

// NodeRoleTypeEquivalents maps a role to the node types that already are that
// role by type: an RDS instance is a Database node and acts as a database
// without anyone saying so. A search by role includes them (SearchNodes), and
// EffectiveRole reads them back. Storage is deliberately absent: it also holds
// persistent disks and file shares, which are not object storage.
var NodeRoleTypeEquivalents = map[string][]NodeType{
	NodeRoleDatabase:     {NodeTypeDatabase},
	NodeRoleCache:        {NodeTypeCache},
	NodeRoleMessageQueue: {NodeTypeMessageQueue, NodeTypeQueue, NodeTypeTopic},
	NodeRoleLoadBalancer: {NodeTypeLoadBalancer},
	NodeRoleAPIGateway:   {NodeTypeAPIGateway},
	NodeRoleMonitoring:   {NodeTypeMonitoringService},
}

// NodeTypesForRoles returns the node types that are, by type, one of roles.
func NodeTypesForRoles(roles []string) []NodeType {
	var out []NodeType
	seen := map[NodeType]bool{}
	for _, r := range roles {
		for _, nt := range NodeRoleTypeEquivalents[r] {
			if !seen[nt] {
				seen[nt] = true
				out = append(out, nt)
			}
		}
	}
	return out
}

// EffectiveRole is the role a node acts as: the role property when it is one
// of the vocabulary, else the role its type implies, else "". Every reader that
// asks "is this a database?" goes through here, so a tagged workload and an RDS
// node answer the same way. A free "role" a custom kind describes itself with
// ("core-switch") is not a vocabulary role and does not count.
func EffectiveRole(nodeType NodeType, properties map[string]interface{}) string {
	if r := getNodeProp(properties, PropRole); IsKnownNodeRole(r) {
		return r
	}
	for role, types := range NodeRoleTypeEquivalents {
		for _, nt := range types {
			if nt == nodeType {
				return role
			}
		}
	}
	return ""
}

// nodeRoleTargetTypes are the node types a user may give a role: things that
// run software (workloads, VMs, services) and every type a user may declare by
// hand, custom kinds included, since a declared kind sits under one of those.
// Infrastructure and metadata (namespaces, clusters, pods, subnets, images,
// users) may not: a Namespace tagged "database" would answer "find all
// databases" and be counted in investigation insights.
var nodeRoleTargetTypes = func() map[NodeType]bool {
	m := map[NodeType]bool{
		NodeTypeWorkload:        true,
		NodeTypeComputeInstance: true,
		NodeTypeService:         true,
		NodeTypeExternalService: true,
		NodeTypeK8sService:      true,
	}
	for _, nt := range ManualNodeTypes {
		m[nt] = true
	}
	return m
}()

// IsNodeRoleTarget reports whether a node of type nt may be given a role.
func IsNodeRoleTarget(nt NodeType) bool {
	return nodeRoleTargetTypes[nt]
}

// NodeRoleTargetTypes lists the node types a role may be set on, for the UI.
func NodeRoleTargetTypes() []NodeType {
	out := make([]NodeType, 0, len(nodeRoleTargetTypes))
	for nt := range nodeRoleTargetTypes {
		out = append(out, nt)
	}
	return out
}

var engineRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._+-]{0,63}$`)

// NormalizeEngine lower-cases and trims a user-typed engine ("PostgreSQL " →
// "postgresql"). An empty engine is valid and means none.
func NormalizeEngine(engine string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(engine))
	if e == "" {
		return "", nil
	}
	if !engineRe.MatchString(e) {
		return "", fmt.Errorf("engine %q must be up to 64 letters, digits, '.', '_', '+' or '-', with no spaces", engine)
	}
	return e, nil
}

// ValidateNodeRole checks a role and engine a user sent and returns the
// engine normalized.
func ValidateNodeRole(role, engine string) (string, error) {
	if !IsKnownNodeRole(role) {
		return "", fmt.Errorf("unknown role %q", role)
	}
	return NormalizeEngine(engine)
}

// NodeRole is one kg_node_roles row.
type NodeRole struct {
	TenantID    string `db:"tenant_id" json:"-"`
	KGNodeID    string `db:"kg_node_id" json:"kg_node_id"`
	UniqueKey   string `db:"unique_key" json:"-"`
	Role        string `db:"role" json:"role"`
	Engine      string `db:"engine" json:"engine,omitempty"`
	SetByUserID string `db:"set_by_user_id" json:"-"`
}

// ApplyUserRole layers a user role onto a node's properties and
// query_attributes, allocating either map when nil, and returns both.
//
// On a node that does not carry a user role yet, the classifier's role and
// engine are kept aside as detected_role / detected_engine. A node that
// already carries one keeps its detected values: its incoming role may be the
// user's own from an earlier write (a resource patch carries stored keys
// forward), not the classifier's. An empty engine falls back to the detected
// one, so a user who only says "database" keeps the postgres icon.
func ApplyUserRole(props, qa map[string]interface{}, role, engine string) (map[string]interface{}, map[string]interface{}) {
	if props == nil {
		props = map[string]interface{}{}
	}
	if qa == nil {
		qa = map[string]interface{}{}
	}
	if getNodeProp(props, PropRoleSource) != RoleSourceUser {
		delete(props, PropDetectedRole)
		delete(props, PropDetectedEngine)
		if r := getNodeProp(props, PropRole); r != "" {
			props[PropDetectedRole] = r
		}
		if e := getNodeProp(props, PropEngine); e != "" {
			props[PropDetectedEngine] = e
		}
	}
	props[PropRole] = role
	if engine == "" {
		engine = getNodeProp(props, PropDetectedEngine)
	}
	if engine != "" {
		props[PropEngine] = engine
	} else {
		delete(props, PropEngine)
	}
	props[PropRoleSource] = RoleSourceUser
	mirrorRole(props, qa)
	return props, qa
}

// ClearUserRole takes a user role off a node and puts back what was detected.
// A node without a user role is left as it is.
func ClearUserRole(props, qa map[string]interface{}) (map[string]interface{}, map[string]interface{}) {
	if getNodeProp(props, PropRoleSource) != RoleSourceUser {
		return props, qa
	}
	if qa == nil {
		qa = map[string]interface{}{}
	}
	restore := func(key, from string) {
		if v := getNodeProp(props, from); v != "" {
			props[key] = v
		} else {
			delete(props, key)
		}
	}
	restore(PropRole, PropDetectedRole)
	restore(PropEngine, PropDetectedEngine)
	delete(props, PropRoleSource)
	delete(props, PropDetectedRole)
	delete(props, PropDetectedEngine)
	mirrorRole(props, qa)
	return props, qa
}

// mirrorRole copies role and engine into query_attributes, so the Attribute
// filter and SearchNodes find a role on any node type, not only the types
// whose schema hoists it.
func mirrorRole(props, qa map[string]interface{}) {
	for _, k := range []string{PropRole, PropEngine} {
		if v := getNodeProp(props, k); v != "" {
			qa[k] = v
		} else {
			delete(qa, k)
		}
	}
}

// ApplyUserRoles layers each node's user role onto it, and takes a stale one
// off a node that no longer has a row (its role was removed while a writer
// carried the old keys forward). rows is keyed by node id. Every writer of
// knowledge_graph_node calls this just before it writes, so a rebuild never
// loses a role (TestEveryNodeWriterAppliesUserRoles).
func ApplyUserRoles(nodes []*DbNode, rows map[string]NodeRole) {
	for _, n := range nodes {
		if n == nil {
			continue
		}
		if r, ok := rows[n.ID]; ok {
			n.Properties, n.QueryAttributes = ApplyUserRole(n.Properties, n.QueryAttributes, r.Role, r.Engine)
			continue
		}
		n.Properties, n.QueryAttributes = ClearUserRole(n.Properties, n.QueryAttributes)
	}
}
