package flow_sources

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// MaxManualDeclarationsBytes caps one declarations document.
const MaxManualDeclarationsBytes = 1 << 20

// MaxManualDeclarationsItems caps the nodes plus dependencies of one
// document. Items are written one after another, each with several lookups,
// so a larger set should be split across documents rather than risk running
// past the request deadline and losing the per-item results.
const MaxManualDeclarationsItems = 500

// ManualDeclarationsDocument is what a user imports: the nodes to declare,
// then the dependencies between them and the rest of the graph.
type ManualDeclarationsDocument struct {
	Nodes        []ManualNodeDeclaration       `json:"nodes"`
	Dependencies []ManualDependencyDeclaration `json:"dependencies"`
}

// ManualNodeDeclaration is a node to declare, with the ref a dependency in
// the same document uses to name it. Ref defaults to the node's name.
type ManualNodeDeclaration struct {
	Ref string `json:"ref,omitempty"`
	ManualNodeCreate
}

// ManualDependencyDeclaration is a dependency whose sides are either refs to
// nodes in the document or endpoints of nodes already in the graph.
type ManualDependencyDeclaration struct {
	Source           ManualDeclarationEndpoint `json:"source"`
	Dest             ManualDeclarationEndpoint `json:"dest"`
	RelationshipType string                    `json:"relationship_type,omitempty"`
	Notes            string                    `json:"notes,omitempty"`
}

// ManualDeclarationEndpoint is one side of a declared dependency: a JSON
// string is a ref to a node in the document, a JSON object an endpoint for a
// node already in the graph.
type ManualDeclarationEndpoint struct {
	Ref      string
	Endpoint *ManualEndpoint
}

// UnmarshalJSON reads a ref string or an endpoint object.
func (e *ManualDeclarationEndpoint) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case len(b) > 0 && b[0] == '"':
		var ref string
		if err := json.Unmarshal(b, &ref); err != nil {
			return err
		}
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return errors.New("a ref may not be empty")
		}
		e.Ref, e.Endpoint = ref, nil
		return nil
	case len(b) > 0 && b[0] == '{':
		// The outer decoder's unknown-field check does not reach into custom
		// unmarshalers, so a typo in an endpoint is refused here.
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		var ep ManualEndpoint
		if err := dec.Decode(&ep); err != nil {
			return err
		}
		e.Ref, e.Endpoint = "", &ep
		return nil
	}
	return errors.New("must be a ref string or an endpoint object")
}

// MarshalJSON writes the side back the way it was declared.
func (e ManualDeclarationEndpoint) MarshalJSON() ([]byte, error) {
	if e.Endpoint != nil {
		return json.Marshal(e.Endpoint)
	}
	return json.Marshal(e.Ref)
}

// ParseManualDeclarations reads a document and runs every check that applies
// to the document as a whole; anything it refuses leaves nothing written.
func ParseManualDeclarations(raw []byte) (*ManualDeclarationsDocument, error) {
	if len(raw) > MaxManualDeclarationsBytes {
		return nil, fmt.Errorf("the document exceeds the %d KB limit", MaxManualDeclarationsBytes/1024)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var doc ManualDeclarationsDocument
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("invalid document: %w", err)
	}
	// More() is false before a stray '}' or ']', so read one more token.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid document: unexpected data after the document")
	}
	if len(doc.Nodes) == 0 && len(doc.Dependencies) == 0 {
		return nil, errors.New("the document declares nothing: add nodes or dependencies")
	}
	if n := len(doc.Nodes) + len(doc.Dependencies); n > MaxManualDeclarationsItems {
		return nil, fmt.Errorf("the document declares %d items; at most %d per import, so split it into several documents", n, MaxManualDeclarationsItems)
	}

	refs := make(map[string]int, len(doc.Nodes))
	for i := range doc.Nodes {
		n := &doc.Nodes[i]
		n.Ref = strings.TrimSpace(n.Ref)
		if n.Ref == "" {
			n.Ref = strings.TrimSpace(n.Name)
		}
		if n.Ref == "" {
			continue // rejected per item: name is required
		}
		if j, dup := refs[n.Ref]; dup {
			return nil, fmt.Errorf("nodes[%d]: ref %q is already used by nodes[%d]", i, n.Ref, j)
		}
		refs[n.Ref] = i
	}
	for i := range doc.Dependencies {
		d := &doc.Dependencies[i]
		for _, side := range []struct {
			name string
			e    *ManualDeclarationEndpoint
		}{{"source", &d.Source}, {"dest", &d.Dest}} {
			if side.e.Endpoint != nil {
				continue
			}
			if side.e.Ref == "" {
				return nil, fmt.Errorf("dependencies[%d].%s is required", i, side.name)
			}
			if _, ok := refs[side.e.Ref]; !ok {
				return nil, fmt.Errorf("dependencies[%d].%s: no node with ref %q in this document", i, side.name, side.e.Ref)
			}
		}
		d.RelationshipType = strings.TrimSpace(d.RelationshipType)
		if d.RelationshipType == "" {
			d.RelationshipType = "CALLS"
		}
	}
	return &doc, nil
}

// ManualNodeDeclarationResult is one node's outcome. NodeID is set when the
// node is in the graph (created, or existing); never on a rejection.
type ManualNodeDeclarationResult struct {
	Index       int                   `json:"index"`
	Ref         string                `json:"ref,omitempty"`
	ID          int64                 `json:"id,omitempty"`
	NodeID      string                `json:"node_id,omitempty"`
	Error       string                `json:"error,omitempty"`
	Node        *ManualNode           `json:"node,omitempty"`
	Declaration ManualNodeDeclaration `json:"declaration"`
}

// ManualNodeDeclarationResults groups the nodes by outcome. Existing holds
// nodes already declared or already collected; dependencies still bind to them.
type ManualNodeDeclarationResults struct {
	Created  []ManualNodeDeclarationResult `json:"created"`
	Existing []ManualNodeDeclarationResult `json:"existing"`
	Rejected []ManualNodeDeclarationResult `json:"rejected"`
}

// ManualDependencyDeclarationResult is one dependency's outcome.
type ManualDependencyDeclarationResult struct {
	Index       int                         `json:"index"`
	ID          int64                       `json:"id,omitempty"`
	Status      string                      `json:"status,omitempty"`
	Error       string                      `json:"error,omitempty"`
	Dependency  *ManualDependency           `json:"dependency,omitempty"`
	Declaration ManualDependencyDeclaration `json:"declaration"`
}

// ManualDependencyDeclarationResults groups the dependencies by outcome.
// Existing holds dependencies that were already declared, so importing the
// same document again reports them the way it reports existing nodes.
type ManualDependencyDeclarationResults struct {
	Created  []ManualDependencyDeclarationResult `json:"created"`
	Existing []ManualDependencyDeclarationResult `json:"existing"`
	Rejected []ManualDependencyDeclarationResult `json:"rejected"`
}

// ManualDeclarationsResult is an import's outcome, item by item.
type ManualDeclarationsResult struct {
	Nodes        ManualNodeDeclarationResults       `json:"nodes"`
	Dependencies ManualDependencyDeclarationResults `json:"dependencies"`
}

// ManualDeclarationsImporter declares a document's nodes, then its
// dependencies, through the two repositories.
type ManualDeclarationsImporter struct {
	nodes *ManualNodeRepository
	deps  *ManualDependencyRepository
	graph ManualNodeGraphWriter
}

// NewManualDeclarationsImporter binds the importer. deps carries the caller's
// account authorizer (WithAccountAuthorizer) when writes must be checked.
func NewManualDeclarationsImporter(nodes *ManualNodeRepository, deps *ManualDependencyRepository, graph ManualNodeGraphWriter) *ManualDeclarationsImporter {
	return &ManualDeclarationsImporter{nodes: nodes, deps: deps, graph: graph}
}

// boundNode is what a ref resolves to once its node is in the graph.
type boundNode struct {
	nodeID, nodeType, name, namespace, accountID string
}

// Import declares the document in order: nodes first, then dependencies. It
// is not transactional; the result says exactly what landed. A node that is
// already declared or already collected is reported as existing and still
// binds the dependencies that ref it. A rejected node binds nothing, and
// carries no node id: a refused caller must not learn whether the resource
// is collected.
func (i *ManualDeclarationsImporter) Import(ctx context.Context, tenantID, userID string, authorize ManualAccountAuthorizer, doc *ManualDeclarationsDocument) (*ManualDeclarationsResult, error) {
	if tenantID == "" {
		return nil, ErrTenantIDRequired
	}
	res := &ManualDeclarationsResult{
		Nodes: ManualNodeDeclarationResults{
			Created: []ManualNodeDeclarationResult{}, Existing: []ManualNodeDeclarationResult{}, Rejected: []ManualNodeDeclarationResult{},
		},
		Dependencies: ManualDependencyDeclarationResults{
			Created: []ManualDependencyDeclarationResult{}, Existing: []ManualDependencyDeclarationResult{}, Rejected: []ManualDependencyDeclarationResult{},
		},
	}
	// One filter-options refresh for the whole import, not one per node.
	nodes := *i.nodes
	nodes.deferFilterInvalidation = true
	defer func() {
		if len(res.Nodes.Created) > 0 {
			i.nodes.invalidateFilterOptions(tenantID)
		}
	}()
	bound := map[string]boundNode{}
	failed := map[string]string{}

	for idx, d := range doc.Nodes {
		if ctx.Err() != nil {
			// The caller went away: stop, and return what already landed so
			// the import is still audited.
			return res, nil
		}
		item := ManualNodeDeclarationResult{Index: idx, Ref: d.Ref, Declaration: d}
		req := d.ManualNodeCreate
		req.TenantID, req.DeclaredByUserID, req.Authorize = tenantID, userID, authorize

		n, err := nodes.prepare(req)
		if err == nil {
			var created *ManualNode
			created, err = nodes.createPrepared(ctx, i.graph, n, authorize)
			var exists *ManualNodeExistsError
			switch {
			case err == nil:
				item.ID, item.NodeID, item.Node = created.ID, created.KGNodeID, created
				res.Nodes.Created = append(res.Nodes.Created, item)
				bound[d.Ref] = boundNode{created.KGNodeID, created.NodeType, created.Name, created.Namespace, created.CloudAccountID}
				continue
			case errors.Is(err, ErrManualNodeDuplicate):
				item.NodeID, item.Error = n.KGNodeID, err.Error()
				if existing, lookupErr := nodes.activeDeclaration(tenantID, n.KGNodeID); lookupErr == nil {
					item.ID, item.Node = existing.ID, existing
				}
				res.Nodes.Existing = append(res.Nodes.Existing, item)
				bound[d.Ref] = boundNode{n.KGNodeID, n.NodeType, n.Name, n.Namespace, n.CloudAccountID}
				continue
			case errors.As(err, &exists):
				item.NodeID, item.Error = exists.NodeID, err.Error()
				res.Nodes.Existing = append(res.Nodes.Existing, item)
				bound[d.Ref] = boundNode{exists.NodeID, n.NodeType, n.Name, n.Namespace, n.CloudAccountID}
				continue
			}
		}
		item.Error = err.Error()
		res.Nodes.Rejected = append(res.Nodes.Rejected, item)
		if d.Ref != "" {
			failed[d.Ref] = err.Error()
		}
	}

	for idx, d := range doc.Dependencies {
		if ctx.Err() != nil {
			// The caller went away: stop, and return what already landed so
			// the import is still audited.
			return res, nil
		}
		item := ManualDependencyDeclarationResult{Index: idx, Declaration: d}
		src, srcPin, err := endpointFor(d.Source, bound, failed)
		if err != nil {
			item.Error = "source: " + err.Error()
			res.Dependencies.Rejected = append(res.Dependencies.Rejected, item)
			continue
		}
		dst, dstPin, err := endpointFor(d.Dest, bound, failed)
		if err != nil {
			item.Error = "dest: " + err.Error()
			res.Dependencies.Rejected = append(res.Dependencies.Rejected, item)
			continue
		}
		dep := ManualDependency{
			TenantID: tenantID, DeclaredByUserID: userID,
			RelationshipType: d.RelationshipType, Notes: d.Notes,
			SourceNodeType: src.NodeType, SourceName: src.Name, SourceNamespace: src.Namespace, SourceCluster: src.Cluster,
			SourceARN: src.ARN, SourceAccountID: src.AccountID, SourceRegion: src.Region,
			DestNodeType: dst.NodeType, DestName: dst.Name, DestNamespace: dst.Namespace, DestCluster: dst.Cluster,
			DestARN: dst.ARN, DestAccountID: dst.AccountID, DestRegion: dst.Region,
		}
		created, err := i.deps.createPinned(ctx, dep, srcPin, dstPin)
		if errors.Is(err, ErrManualDependencyDuplicate) {
			// The wrapped error tells a form user to edit or delete the row;
			// for a re-imported document, already declared is the answer.
			item.Error = ErrManualDependencyDuplicate.Error()
			res.Dependencies.Existing = append(res.Dependencies.Existing, item)
			continue
		}
		if err != nil {
			item.Error = err.Error()
			res.Dependencies.Rejected = append(res.Dependencies.Rejected, item)
			continue
		}
		item.ID, item.Status, item.Dependency = created.ID, created.ResolutionStatus, created
		res.Dependencies.Created = append(res.Dependencies.Created, item)
	}
	return res, nil
}

// endpointFor is the endpoint text a side is stored with, plus the node id
// it is pinned to when the side refs a node of the document. A ref'd side
// keeps the node's own type, name, namespace and account, so it re-resolves to
// the same node on its own; the pin makes it exact.
func endpointFor(e ManualDeclarationEndpoint, bound map[string]boundNode, failed map[string]string) (ManualEndpoint, string, error) {
	if e.Endpoint != nil {
		return *e.Endpoint, "", nil
	}
	b, ok := bound[e.Ref]
	if !ok {
		if reason, rejected := failed[e.Ref]; rejected {
			return ManualEndpoint{}, "", fmt.Errorf("node %q was not declared: %s", e.Ref, reason)
		}
		return ManualEndpoint{}, "", fmt.Errorf("node %q was not declared", e.Ref)
	}
	return ManualEndpoint{NodeType: b.nodeType, Name: b.name, Namespace: b.namespace, AccountID: b.accountID}, b.nodeID, nil
}

// activeDeclaration is the live declaration of a node, or sql.ErrNoRows.
func (r *ManualNodeRepository) activeDeclaration(tenantID, kgNodeID string) (*ManualNode, error) {
	rows, err := r.query(`SELECT `+manualNodeColumns+` FROM kg_manual_nodes
		WHERE tenant_id = $1 AND kg_node_id = $2 AND is_active = true`, tenantID, kgNodeID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, sql.ErrNoRows
	}
	return &rows[0], nil
}
