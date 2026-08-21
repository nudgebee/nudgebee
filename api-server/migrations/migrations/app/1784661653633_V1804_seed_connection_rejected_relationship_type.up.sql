-- CONNECTION_REJECTED: src → dst connection attempt blocked (REJECT rows in VPC
-- Flow Logs). This is the security view of the flow-derived service map —
-- attempted-but-blocked reachability, kept separate from CALLS (ACCEPT = live
-- dependency) so a blocked connection is never mistaken for a real dependency.
-- Emitted by the aws-vpc-flow knowledge-graph flow source.
INSERT INTO knowledge_graph_relationship_types (name, value)
VALUES
    ('CONNECTION_REJECTED', 'CONNECTION_REJECTED')
ON CONFLICT (name) DO NOTHING;
