package sources

import (
	"fmt"
	"log/slog"

	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"
)

func init() {
	RegisterCrossSourceEnricherFactory("identity", func(logger *slog.Logger) core.CrossSourceEnricherInterface {
		return NewIdentityEnricher(logger)
	}, "Creates canonical NudgebeeUser nodes from the users table and links every per-integration identity (GitHubUser, GitLabUser, SlackUser, ...) to its mapped Nudgebee user via SAME_AS")

	core.RegisterSpecificTypeSchema(nudgebeeUserSchema)
}

// nudgebeeUserSchema is the concrete property schema for the canonical NudgebeeUser
// specific_type — the internal `users` table row, materialized as a KG node so every
// per-integration identity (GitHubUser today; GitLabUser/SlackUser/... as those
// integrations join the KG) has one stable node to link to via SAME_AS.
var nudgebeeUserSchema = core.SpecificTypeSchema{
	SpecificType: "NudgebeeUser",
	NodeType:     core.NodeTypeUserAccount,
	Properties: []core.PropertyDef{
		{Name: "username", Indexed: true},
		{Name: "nudgebee_user_id", Indexed: true},
	},
}

// integrationIdentitySpecificTypes maps an integration_user_accounts.integration_type
// value to the KG specific_type its per-integration identity nodes use. A node is
// matched against one integration_user_accounts row by (username, integration instance)
// — see identityKey. Adding a new integration to the KG (e.g. Slack) needs exactly one
// entry here — no other enricher changes, as long as that source's identity nodes carry
// a `username` property holding the same login/id the Identity Sync cron records, and
// set node CloudAccountID to the integrations.id (the pattern every source in this
// package already follows for its per-instance dispatch key).
var integrationIdentitySpecificTypes = map[string]string{
	"github":    "GitHubUser",
	"gitlab":    "GitLabUser",
	"pagerduty": "PagerDutyUser",
}

// IdentityEnricher builds the canonical identity layer of the Knowledge Graph:
//  1. One NudgebeeUser node per active user in the tenant (from `users`+`tenant_users`).
//  2. A SAME_AS edge from every already-built per-integration identity node (GitHubUser
//     today) to its NudgebeeUser node, driven by whatever an admin has manually mapped
//     (or the Identity Sync cron auto-matched by email) in integration_user_accounts —
//     see api-server/services/user/identity_sync.go.
//
// Runs in Phase 2.1 (cross-source enrichment), after all per-account/per-integration
// sources have built their nodes, so every per-integration identity node produced this
// cycle already exists in allNodes by the time this runs.
type IdentityEnricher struct {
	logger *slog.Logger
}

// NewIdentityEnricher creates a new identity enricher.
func NewIdentityEnricher(logger *slog.Logger) *IdentityEnricher {
	if logger == nil {
		logger = slog.Default()
	}
	return &IdentityEnricher{logger: logger}
}

// GetName returns the name of the enricher.
func (e *IdentityEnricher) GetName() string { return "identity" }

// EnrichCrossSources adds canonical NudgebeeUser nodes and SAME_AS edges to every
// per-integration identity node this build cycle produced.
func (e *IdentityEnricher) EnrichCrossSources(
	_ *security.RequestContext,
	allNodes []*core.DbNode,
	allEdges []*core.DbEdge,
	tenantID string,
) ([]*core.DbNode, []*core.DbEdge, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return allNodes, allEdges, fmt.Errorf("identity_enricher: failed to get database manager: %w", err)
	}

	canonicalNodes, canonicalByUserID, err := e.buildCanonicalUserNodes(dbms, tenantID)
	if err != nil {
		e.logger.Warn("identity_enricher: failed to build canonical user nodes, skipping", "error", err)
		return allNodes, allEdges, nil
	}
	if len(canonicalNodes) == 0 {
		return allNodes, allEdges, nil
	}

	mappings, err := e.fetchMappings(dbms, tenantID)
	if err != nil {
		e.logger.Warn("identity_enricher: failed to fetch integration user mappings, skipping edges", "error", err)
		return append(allNodes, canonicalNodes...), allEdges, nil
	}

	identityIndex := indexIdentityNodes(allNodes)
	newEdges := buildSameAsEdges(canonicalByUserID, mappings, identityIndex, tenantID)

	return append(allNodes, canonicalNodes...), append(allEdges, newEdges...), nil
}

// buildSameAsEdges links each mapped external identity to its canonical NudgebeeUser
// node. Pulled out of EnrichCrossSources as a pure function (no DB access) so the
// matching logic — which mapping resolves to which edge, and why a given row is
// skipped — is unit-testable without a database.
func buildSameAsEdges(
	canonicalByUserID map[string]*core.DbNode,
	mappings []mappingRow,
	identityIndex map[identityKey]*core.DbNode,
	tenantID string,
) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0, len(mappings))
	for _, m := range mappings {
		canonicalNode, ok := canonicalByUserID[m.mappedUserID]
		if !ok {
			continue // mapped user isn't active/in this tenant this cycle
		}
		specificType, known := integrationIdentitySpecificTypes[m.integrationType]
		if !known {
			continue // this integration isn't wired into the KG yet
		}
		identityNode, ok := identityIndex[identityKey{specificType, m.externalUserID, m.integrationID}]
		if !ok {
			continue // that identity node wasn't built this cycle (e.g. access revoked)
		}
		edges = append(edges, core.NewEdge(
			identityNode.ID, canonicalNode.ID, core.RelationshipSameAs,
			map[string]interface{}{}, tenantID, tenantID, "identity",
		))
	}
	return edges
}

// identityKey indexes a per-integration identity node by its concrete specific_type,
// login/username, and the specific integration instance (node.CloudAccountID — the
// integrations.id, per BuildGraph's doc comment in each source) it was built from, so
// it can be matched against one specific integration_user_accounts row. The instance
// must be part of the key: a tenant can configure more than one integration of the
// same type (e.g. two PagerDuty accounts), and if the same external user shows up in
// both, they'd otherwise collide on (specific_type, username) and only one of the two
// identity nodes would ever get a SAME_AS edge, silently dropping the other.
type identityKey struct {
	specificType  string
	username      string
	integrationID string
}

// indexIdentityNodes indexes every node whose specific_type is a known
// per-integration identity type (see integrationIdentitySpecificTypes) by
// (specific_type, username, integration instance).
func indexIdentityNodes(allNodes []*core.DbNode) map[identityKey]*core.DbNode {
	knownSpecificTypes := make(map[string]bool, len(integrationIdentitySpecificTypes))
	for _, st := range integrationIdentitySpecificTypes {
		knownSpecificTypes[st] = true
	}

	index := make(map[identityKey]*core.DbNode)
	for _, n := range allNodes {
		if !knownSpecificTypes[n.SpecificType] {
			continue
		}
		username, _ := n.Properties["username"].(string)
		if username == "" {
			continue
		}
		index[identityKey{n.SpecificType, username, n.CloudAccountID}] = n
	}
	return index
}

// buildCanonicalUserNodes creates one NudgebeeUser node per active user in the
// tenant, keyed by users.id for edge-target lookup. The unique key's Name component
// is the user's username (email) — unique per the `users_username_key` DB constraint,
// so this never collides across users the way a display name could.
func (e *IdentityEnricher) buildCanonicalUserNodes(dbms *database.DatabaseManager, tenantID string) ([]*core.DbNode, map[string]*core.DbNode, error) {
	rows, err := dbms.Query(`
		SELECT u.id::text, u.username::text, u.display_name
		FROM users u
		JOIN tenant_users tu ON tu.user = u.id
		WHERE tu.tenant = $1 AND u.status = 'active'
	`, tenantID)
	if err != nil {
		return nil, nil, fmt.Errorf("query tenant users: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			e.logger.Warn("identity_enricher: failed to close user rows", "error", cerr)
		}
	}()

	var nodes []*core.DbNode
	byUserID := make(map[string]*core.DbNode)
	for rows.Next() {
		var userID, username, displayName string
		if err := rows.Scan(&userID, &username, &displayName); err != nil {
			return nil, nil, fmt.Errorf("scan tenant user row: %w", err)
		}
		name := displayName
		if name == "" {
			name = username
		}
		properties := map[string]interface{}{
			"specific_type":    "NudgebeeUser",
			"name":             name,
			"username":         username,
			"nudgebee_user_id": userID,
		}
		uniqueKey := core.BuildUniqueKey(core.CloudProviderExternal, tenantID, "", core.NodeTypeUserAccount, "", username)
		node := core.NewNode(core.NodeTypeUserAccount, uniqueKey, properties, tenantID, tenantID, "identity")
		nodes = append(nodes, node)
		byUserID[userID] = node
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate tenant user rows: %w", err)
	}
	return nodes, byUserID, nil
}

// mappingRow is one integration_user_accounts row linking an external identity to a
// Nudgebee user. integrationID is that row's integration_id — a tenant can configure
// more than one integration of the same type, and this is what disambiguates which
// one's identity node this row should link (see identityKey).
type mappingRow struct {
	integrationType string
	externalUserID  string
	mappedUserID    string
	integrationID   string
}

// fetchMappings loads every active, mapped external identity for the tenant, across
// all integration types (not just GitHub) — indexIdentityNodes/integrationIdentitySpecificTypes
// filters down to the ones the KG actually has nodes for this cycle.
func (e *IdentityEnricher) fetchMappings(dbms *database.DatabaseManager, tenantID string) ([]mappingRow, error) {
	rows, err := dbms.Query(`
		SELECT integration_type, external_user_id, mapped_user_id::text, COALESCE(integration_id::text, '')
		FROM integration_user_accounts
		WHERE tenant_id = $1 AND is_active = true AND mapped_user_id IS NOT NULL
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query integration user mappings: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			e.logger.Warn("identity_enricher: failed to close mapping rows", "error", cerr)
		}
	}()

	var results []mappingRow
	for rows.Next() {
		var m mappingRow
		if err := rows.Scan(&m.integrationType, &m.externalUserID, &m.mappedUserID, &m.integrationID); err != nil {
			return nil, fmt.Errorf("scan integration user mapping row: %w", err)
		}
		results = append(results, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate integration user mapping rows: %w", err)
	}
	return results, nil
}
