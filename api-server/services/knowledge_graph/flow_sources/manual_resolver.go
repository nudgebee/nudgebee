package flow_sources

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jmoiron/sqlx"

	"nudgebee/services/internal/database"
)

// ManualFlowSourceName is the canonical name stamped on edges and rows owned by
// the user-declared dependency flow source.
const ManualFlowSourceName = "manual"

// MaxManualMatchCandidates caps how many candidates we surface for an
// ambiguous match. Beyond this we refuse to store the candidate list and
// flip the row to *_too_many_matches — the operator must add qualifiers
// (namespace, cluster, arn) and re-resolve.
const MaxManualMatchCandidates = 10

// Resolution status values stored on kg_manual_dependencies.resolution_status.
// Mirrors the DB CHECK constraint in V743.
const (
	ManualResolutionPending              = "pending"
	ManualResolutionResolved             = "resolved"
	ManualResolutionSourceUnmatched      = "source_unmatched"
	ManualResolutionDestUnmatched        = "dest_unmatched"
	ManualResolutionSourceAmbiguous      = "source_ambiguous"
	ManualResolutionDestAmbiguous        = "dest_ambiguous"
	ManualResolutionSourceTooManyMatches = "source_too_many_matches"
	ManualResolutionDestTooManyMatches   = "dest_too_many_matches"
	ManualResolutionInvalidPayload       = "invalid_payload"
	ManualResolutionNodeInactive         = "node_inactive"
)

// EndpointSide identifies which side of the dependency a result belongs to
// when the resolver propagates a per-endpoint result into the row-level
// resolution_status (e.g. ambiguous on source vs ambiguous on dest).
type EndpointSide string

const (
	EndpointSideSource EndpointSide = "source"
	EndpointSideDest   EndpointSide = "dest"
)

// ManualEndpoint is the set of fields a user can supply to identify one side
// of a declared dependency. NodeType + Name are required (validated above the
// resolver); the rest are optional qualifiers used to narrow the match.
type ManualEndpoint struct {
	NodeType  string
	Name      string
	Namespace string
	Cluster   string
	ARN       string
	AccountID string
	Region    string
}

// ManualMatchCandidate is what the operator (or UI) sees when a row is
// ambiguous: enough metadata to pick the right node without a second lookup.
type ManualMatchCandidate struct {
	NodeID      string `json:"node_id"`
	NodeType    string `json:"node_type"`
	Namespace   string `json:"namespace,omitempty"`
	Cluster     string `json:"cluster,omitempty"`
	ARN         string `json:"arn,omitempty"`
	DisplayName string `json:"display_name"`
}

// EndpointResolveStatus is the per-endpoint resolver outcome. The row-level
// resolution_status is derived by combining source + dest endpoint statuses.
type EndpointResolveStatus string

const (
	EndpointStatusResolved       EndpointResolveStatus = "resolved"
	EndpointStatusUnmatched      EndpointResolveStatus = "unmatched"
	EndpointStatusAmbiguous      EndpointResolveStatus = "ambiguous"
	EndpointStatusTooManyMatches EndpointResolveStatus = "too_many_matches"
	EndpointStatusInvalidPayload EndpointResolveStatus = "invalid_payload"
)

// EndpointResolveResult carries the outcome for one side of a row.
type EndpointResolveResult struct {
	Status       EndpointResolveStatus
	NodeID       string
	Candidates   []ManualMatchCandidate
	MatchCount   int
	ErrorMessage string
}

// ManualEndpointResolver answers "which existing KG node does this endpoint
// reference?" using direct SQL against knowledge_graph_node. It is invoked at
// upload time (RPC handler), on operator-triggered re-resolves, and once per
// build cycle by the manual flow source.
type ManualEndpointResolver struct {
	dbManager *database.DatabaseManager
	logger    *slog.Logger
}

// NewManualEndpointResolver constructs a resolver bound to the given DB.
func NewManualEndpointResolver(dbManager *database.DatabaseManager, logger *slog.Logger) *ManualEndpointResolver {
	if logger == nil {
		logger = slog.Default()
	}
	return &ManualEndpointResolver{dbManager: dbManager, logger: logger}
}

// Resolve runs the loose-match contract: node_type + name required, all other
// qualifiers optional. Match buckets:
//
//	0       -> unmatched
//	1       -> resolved (NodeID populated)
//	2..10   -> ambiguous (Candidates populated)
//	>10     -> too_many_matches (MatchCount populated, Candidates empty)
//
// The query caps at MaxManualMatchCandidates+1 rows so we can detect the
// too-many case without scanning the full result.
func (r *ManualEndpointResolver) Resolve(ctx context.Context, tenantID string, endpoint ManualEndpoint) (*EndpointResolveResult, error) {
	if endpoint.NodeType == "" || endpoint.Name == "" {
		return &EndpointResolveResult{
			Status:       EndpointStatusInvalidPayload,
			ErrorMessage: "node_type and name are required",
		}, nil
	}

	query, args := r.buildQuery(tenantID, endpoint)
	rows, err := r.dbManager.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("manual resolver query failed: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			r.logger.Warn("manual resolver row close failed", "error", cerr)
		}
	}()

	candidates, err := scanCandidates(rows)
	if err != nil {
		return nil, err
	}

	return classifyCandidates(candidates), nil
}

// buildQuery composes the loose-match SQL with only the qualifiers the
// operator supplied. tenant_id and node_type are always pinned; is_active is
// always true (inactive nodes must not satisfy a manual reference).
func (r *ManualEndpointResolver) buildQuery(tenantID string, e ManualEndpoint) (string, []interface{}) {
	var sb strings.Builder
	sb.WriteString(`SELECT id::text,
		node_type,
		COALESCE(query_attributes->>'name', properties->>'name', '') AS name,
		COALESCE(query_attributes->>'namespace', '') AS namespace,
		COALESCE(query_attributes->>'cluster', '') AS cluster,
		COALESCE(properties->>'arn', '') AS arn
		FROM knowledge_graph_node
		WHERE tenant_id = $1 AND is_active = true AND node_type = $2`)
	args := []interface{}{tenantID, e.NodeType}
	idx := 3

	// ARN wins when present: it's the deterministic AWS identifier.
	// query_attributes['arn'] is mostly NULL in current data, so we go via
	// properties['arn']. This also incidentally matches Azure resource IDs
	// (which AWSSource stores in the same property).
	if e.ARN != "" {
		fmt.Fprintf(&sb, " AND properties->>'arn' = $%d", idx)
		args = append(args, e.ARN)
		idx++
	} else {
		fmt.Fprintf(&sb, " AND (query_attributes->>'name' = $%d OR properties->>'name' = $%d)", idx, idx)
		args = append(args, e.Name)
		idx++
	}

	if e.Namespace != "" {
		fmt.Fprintf(&sb, " AND query_attributes->>'namespace' = $%d", idx)
		args = append(args, e.Namespace)
		idx++
	}
	if e.Cluster != "" {
		fmt.Fprintf(&sb, " AND query_attributes->>'cluster' = $%d", idx)
		args = append(args, e.Cluster)
		idx++
	}
	if e.AccountID != "" {
		// Strict match: when the operator supplies an account_id, treat it as
		// a hard qualifier. The earlier `OR aws_account_number IS NULL` leg
		// silently matched every non-AWS node (where the property is
		// universally NULL), defeating the qualifier entirely.
		// Operators declaring an AWS-side endpoint should benefit from the
		// narrowing; operators who accidentally fill account_id for a K8s
		// endpoint will get an unmatched status and learn to leave it blank.
		fmt.Fprintf(&sb, " AND query_attributes->>'aws_account_number' = $%d", idx)
		args = append(args, e.AccountID)
		// idx is no longer used past this point; LIMIT below doesn't take a
		// bind param. Don't increment — golangci-lint flags it as
		// ineffectual.
	}

	// LIMIT MaxManualMatchCandidates+1 lets us distinguish "10 candidates" from
	// ">10" without scanning more rows than we need.
	fmt.Fprintf(&sb, " LIMIT %d", MaxManualMatchCandidates+1)
	return sb.String(), args
}

// scanCandidates pulls all matched rows into ManualMatchCandidate. Display
// name is composed from the most informative fields so an operator picking
// from an ambiguous list has enough context.
func scanCandidates(rows *sqlx.Rows) ([]ManualMatchCandidate, error) {
	candidates := make([]ManualMatchCandidate, 0, MaxManualMatchCandidates+1)
	for rows.Next() {
		var c ManualMatchCandidate
		var name string
		if err := rows.Scan(&c.NodeID, &c.NodeType, &name, &c.Namespace, &c.Cluster, &c.ARN); err != nil {
			return nil, fmt.Errorf("manual resolver row scan failed: %w", err)
		}
		c.DisplayName = buildDisplayName(name, c.Namespace, c.Cluster, c.ARN, c.NodeType)
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("manual resolver row iteration failed: %w", err)
	}
	return candidates, nil
}

// classifyCandidates buckets the SQL result by count.
func classifyCandidates(candidates []ManualMatchCandidate) *EndpointResolveResult {
	switch {
	case len(candidates) == 0:
		return &EndpointResolveResult{Status: EndpointStatusUnmatched, MatchCount: 0}
	case len(candidates) == 1:
		return &EndpointResolveResult{
			Status:     EndpointStatusResolved,
			NodeID:     candidates[0].NodeID,
			MatchCount: 1,
		}
	case len(candidates) <= MaxManualMatchCandidates:
		return &EndpointResolveResult{
			Status:     EndpointStatusAmbiguous,
			Candidates: candidates,
			MatchCount: len(candidates),
		}
	default:
		return &EndpointResolveResult{
			Status:     EndpointStatusTooManyMatches,
			MatchCount: len(candidates),
			ErrorMessage: fmt.Sprintf(
				"more than %d nodes matched; add namespace, cluster, or arn to narrow",
				MaxManualMatchCandidates),
		}
	}
}

// buildDisplayName composes a human-readable label for an ambiguous-row
// candidate. ARN takes precedence (most specific), then namespace/cluster
// for K8s nodes, then bare name.
func buildDisplayName(name, namespace, cluster, arn, nodeType string) string {
	if arn != "" {
		return fmt.Sprintf("%s (%s)", name, arn)
	}
	if namespace != "" && cluster != "" {
		return fmt.Sprintf("%s (%s/%s @ %s)", name, namespace, nodeType, cluster)
	}
	if namespace != "" {
		return fmt.Sprintf("%s (%s/%s)", name, namespace, nodeType)
	}
	return fmt.Sprintf("%s (%s)", name, nodeType)
}

// RowResolutionFromEndpoints folds per-endpoint outcomes into the row-level
// resolution_status used by kg_manual_dependencies. Source endpoint failures
// take precedence over dest endpoint failures when both fail in the same
// category, mirroring the UI mental model "fix source first".
func RowResolutionFromEndpoints(src, dst *EndpointResolveResult) string {
	switch {
	case src.Status == EndpointStatusInvalidPayload || dst.Status == EndpointStatusInvalidPayload:
		return ManualResolutionInvalidPayload
	case src.Status == EndpointStatusUnmatched:
		return ManualResolutionSourceUnmatched
	case dst.Status == EndpointStatusUnmatched:
		return ManualResolutionDestUnmatched
	case src.Status == EndpointStatusTooManyMatches:
		return ManualResolutionSourceTooManyMatches
	case dst.Status == EndpointStatusTooManyMatches:
		return ManualResolutionDestTooManyMatches
	case src.Status == EndpointStatusAmbiguous:
		return ManualResolutionSourceAmbiguous
	case dst.Status == EndpointStatusAmbiguous:
		return ManualResolutionDestAmbiguous
	default:
		return ManualResolutionResolved
	}
}
