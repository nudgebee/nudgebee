-- Knowledge Graph Agent Traversal Indexes
-- These indexes support the kg_search_nodes and kg_traverse Hasura actions
-- used by the AI agent for infrastructure topology exploration.
--
-- Write overhead note: At current scale (30K nodes, 18K edges) the impact on
-- nightly BuildGraphs batch upserts is negligible. Monitor build duration if
-- graph exceeds 200K nodes — consider dropping JSONB expression indexes in
-- favor of a materialized search view if needed.

-- Node search by name (most common agent query)
CREATE INDEX IF NOT EXISTS idx_kg_node_qa_name
    ON knowledge_graph_node ((query_attributes->>'name'))
    WHERE is_active = true;

-- Node search by name + namespace combo
CREATE INDEX IF NOT EXISTS idx_kg_node_qa_name_ns
    ON knowledge_graph_node ((query_attributes->>'name'), (query_attributes->>'namespace'))
    WHERE is_active = true;

-- Edge traversal: source side (downstream direction)
CREATE INDEX IF NOT EXISTS idx_kg_edge_source
    ON knowledge_graph_edge (source_node_id);

-- Edge traversal: destination side (upstream direction)
CREATE INDEX IF NOT EXISTS idx_kg_edge_dest
    ON knowledge_graph_edge (destination_node_id);

-- Edge relationship type filtering
CREATE INDEX IF NOT EXISTS idx_kg_edge_rel_type
    ON knowledge_graph_edge (relationship_type);
