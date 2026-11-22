-- CONNECTED_TO: an undirected physical/network link (a switch cabled to a
-- server), declared by users on manual dependencies that touch a node declared
-- outside any connected account. kg_manual_dependencies.relationship_type and
-- knowledge_graph_edge.relationship_type both reference this table.
INSERT INTO knowledge_graph_relationship_types (name, value)
VALUES
    ('CONNECTED_TO', 'CONNECTED_TO')
ON CONFLICT (name) DO NOTHING;
