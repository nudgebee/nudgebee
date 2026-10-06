package flow_sources

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"nudgebee/services/knowledge_graph/core"
)

// Manually declared nodes.
//
// A user declares a node so a manual dependency can point at a resource no
// collector has discovered. Two kinds:
//
//   - In a connected cloud account: the cloud's own builder (registered in
//     core by sources/<cloud>) makes the node from a synthetic inventory row,
//     so it gets the node id the collector writes for the same resource. When
//     the collector ingests it, the declaration is adopted, not duplicated.
//   - Outside any connected account (on-prem, third-party): tenant-scoped,
//     keyed external:<tenant>::<type>:<namespace>:<name> like the identity
//     nodes. No collector can produce that key, so these never merge; the
//     duplicate check flags a look-alike instead.
//
// The table keeps what the builder produced (unique key, base properties), and
// the manual flow source re-emits every live declaration from it each build.

// Declaration statuses (kg_manual_nodes.status; mirrors the CHECK in V989).
const (
	ManualNodeStatusActive            = "active"
	ManualNodeStatusAdopted           = "adopted"
	ManualNodeStatusPossibleDuplicate = "possible_duplicate"
)

// TenantScopedProvider is the cloud_provider a declaration outside any
// connected account is listed under in the schema response.
const TenantScopedProvider = "external"

// tenantScopedProperties are the optional properties a declaration outside a
// connected account may carry: no concrete schema applies to it.
var tenantScopedProperties = []core.ManualNodeProperty{{Name: "endpoint"}, {Name: "engine"}, {Name: "version"}}

// maxManualNodeLabels caps labels per node; a declaration is not a tag store.
const maxManualNodeLabels = 50

var (
	// ErrManualNodeDuplicate: the resource already has an active declaration.
	ErrManualNodeDuplicate = errors.New("this resource is already declared")
	// ErrManualNodeAdopted: a collector now writes this node, so it is no
	// longer the declaration's to edit or merge.
	ErrManualNodeAdopted = errors.New("a collector now provides this node")
	// ErrManualNodeForbidden: the caller may not write declarations in the
	// account (or, for one outside any account, in the tenant).
	ErrManualNodeForbidden = errors.New("you do not have write access to this account")
)

// ManualAccountAuthorizer reports whether the caller may write a manual node
// or dependency in cloudAccountID; "" is something outside any connected
// account (a tenant-scoped node), which belongs to the tenant as a whole.
type ManualAccountAuthorizer func(cloudAccountID string) bool

// ManualNodeExistsError is returned when the resource is already in the graph
// as a collected node: the user should point dependencies at that node.
type ManualNodeExistsError struct {
	NodeID string
	Source string
}

func (e *ManualNodeExistsError) Error() string {
	return fmt.Sprintf("this resource is already in the graph (node %s, from %s); declare dependencies against it instead", e.NodeID, e.Source)
}

// ManualNode is one declaration, as returned to the UI.
type ManualNode struct {
	ID               int64                  `json:"id"`
	TenantID         string                 `json:"tenant_id"`
	CloudAccountID   string                 `json:"cloud_account_id,omitempty"`
	CloudProvider    string                 `json:"cloud_provider,omitempty"`
	NodeType         string                 `json:"node_type"`
	SpecificType     string                 `json:"specific_type"`
	Name             string                 `json:"name"`
	Region           string                 `json:"region,omitempty"`
	Namespace        string                 `json:"namespace,omitempty"`
	NetworkNodeID    string                 `json:"network_node_id,omitempty"`
	Description      string                 `json:"description,omitempty"`
	Properties       map[string]interface{} `json:"properties,omitempty"`
	Labels           map[string]string      `json:"labels,omitempty"`
	KGNodeID         string                 `json:"kg_node_id"`
	UniqueKey        string                 `json:"unique_key"`
	Status           string                 `json:"status"`
	DuplicateNodeID  string                 `json:"duplicate_node_id,omitempty"`
	DeclaredByUserID string                 `json:"declared_by_user_id,omitempty"`
	CreatedAt        time.Time              `json:"created_at"`
	UpdatedAt        time.Time              `json:"updated_at"`

	baseProperties map[string]interface{}
}

// IsTenantScoped reports whether the declaration lives outside any connected
// cloud account.
func (n *ManualNode) IsTenantScoped() bool { return n.CloudAccountID == "" }

// accountSlot is the node's cloud_account_id: the account, or the tenant for
// a tenant-scoped node (a partition key only; it has no FK).
func (n *ManualNode) accountSlot() string {
	if n.IsTenantScoped() {
		return n.TenantID
	}
	return n.CloudAccountID
}

// details is the descriptive data layered onto the node.
func (n *ManualNode) details() core.ManualNodeDetails {
	return core.ManualNodeDetails{
		ManualNodeID: n.ID,
		Description:  n.Description,
		Properties:   n.Properties,
		Labels:       n.Labels,
	}
}

// Materialize is the graph node for this declaration, identical on every call.
func (n *ManualNode) Materialize() (*core.DbNode, error) {
	return core.MaterializeManualNode(core.NodeType(n.NodeType), n.SpecificType, n.UniqueKey,
		n.baseProperties, n.details(), n.TenantID, n.accountSlot())
}

// ManualNodeDetailsPatch is everything Update may change. Identity fields are
// absent on purpose: changing one changes the node id.
type ManualNodeDetailsPatch struct {
	Description string                 `json:"description"`
	Properties  map[string]interface{} `json:"properties"`
	Labels      map[string]string      `json:"labels"`
}

// TenantScopedManualNodeKinds is the schema for declarations outside any
// connected account.
func TenantScopedManualNodeKinds() []core.ManualNodeKind {
	out := make([]core.ManualNodeKind, 0, len(core.ManualNodeTypes))
	for _, nt := range core.ManualNodeTypes {
		out = append(out, core.ManualNodeKind{
			SpecificType: string(nt),
			NodeType:     nt,
			KeyFields: []core.ManualNodeField{
				{Name: core.ManualNodeFieldName, Label: "Name", Hint: "How the resource is known, e.g. billing-db or api.stripe.com", Required: true},
				{Name: core.ManualNodeFieldNamespace, Label: "Namespace", Hint: "Optional grouping, e.g. the team or environment"},
			},
			Properties: append([]core.ManualNodeProperty(nil), tenantScopedProperties...),
		})
	}
	return out
}

// buildTenantScopedNode makes the base node for a declaration outside any
// connected account.
func buildTenantScopedNode(n *ManualNode) (*core.DbNode, error) {
	nt := core.NodeType(n.NodeType)
	if !core.IsManualTenantScopedNodeType(nt) {
		return nil, fmt.Errorf("node type %q cannot be declared", n.NodeType)
	}
	if n.Name == "" {
		return nil, errors.New("name is required")
	}
	if strings.Contains(n.Name, ":") || strings.Contains(n.Namespace, ":") {
		// The unique key is ':'-delimited and parsed back by position.
		return nil, errors.New("name and namespace may not contain ':'")
	}
	props := map[string]interface{}{"name": n.Name}
	if n.Namespace != "" {
		props["namespace"] = n.Namespace
	}
	if n.Region != "" {
		props["region"] = n.Region
	}
	key := core.BuildUniqueKey(core.CloudProviderExternal, n.TenantID, "", nt, n.Namespace, n.Name)
	return core.NewNode(nt, key, props, n.TenantID, n.TenantID, core.ManualNodeSource), nil
}

// validateManualNodeDetails checks the descriptive data against what the type
// allows: its concrete schema in a cloud account, a fixed few fields outside.
func validateManualNodeDetails(n *ManualNode) error {
	shapes := manualPropertyShapes(n)
	var unknown []string
	for k, v := range n.Properties {
		shape, ok := shapes[k]
		if !ok {
			if !n.IsTenantScoped() && core.IsManualObservedProperty(k) {
				return fmt.Errorf("%s is read from the resource when it is collected, so it cannot be declared", k)
			}
			unknown = append(unknown, k)
			continue
		}
		if err := checkPropertyShape(k, v, shape); err != nil {
			return err
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("%s does not have the properties %s", n.SpecificType, strings.Join(unknown, ", "))
	}
	if len(n.Labels) > maxManualNodeLabels {
		return fmt.Errorf("at most %d labels", maxManualNodeLabels)
	}
	for k := range n.Labels {
		if strings.TrimSpace(k) == "" {
			return errors.New("label keys may not be empty")
		}
	}
	return nil
}

// manualPropertyShapes maps each property the declaration's type allows to the
// shape its value must have.
func manualPropertyShapes(n *ManualNode) map[string]core.PropertyShape {
	allowed := tenantScopedProperties
	if !n.IsTenantScoped() {
		allowed = core.SchemaProperties(n.SpecificType)
	}
	out := make(map[string]core.PropertyShape, len(allowed))
	for _, p := range allowed {
		out[p.Name] = p.Shape
	}
	return out
}

// checkPropertyShape checks a value against the shape the collector stores the
// property in, so a declared node reads like a collected one.
func checkPropertyShape(name string, v interface{}, shape core.PropertyShape) error {
	switch shape {
	case core.PropertyShapeMap:
		m, ok := v.(map[string]interface{})
		if !ok {
			return fmt.Errorf("property %q must be a set of key/value pairs", name)
		}
		for k, e := range m {
			if _, ok := e.(string); !ok || strings.TrimSpace(k) == "" {
				return fmt.Errorf("property %q must map non-empty keys to text values", name)
			}
		}
	case core.PropertyShapeList:
		l, ok := v.([]interface{})
		if !ok {
			return fmt.Errorf("property %q must be a list of values", name)
		}
		for _, e := range l {
			if s, ok := e.(string); !ok || strings.TrimSpace(s) == "" {
				return fmt.Errorf("property %q must be a list of non-empty text values", name)
			}
		}
	default:
		switch v.(type) {
		case string, bool, float64, int, int64, nil:
		default:
			return fmt.Errorf("property %q must be a string, number or boolean", name)
		}
	}
	return nil
}
