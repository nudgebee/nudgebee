-- HAS_ACCESS_TO: ServiceIdentity → data resource (BigQuery/Storage/Firestore/…)
-- edges derived from GCP IAM policy role bindings (createHasAccessEdges).
INSERT INTO knowledge_graph_relationship_types (name, value)
VALUES
    ('HAS_ACCESS_TO', 'HAS_ACCESS_TO')
ON CONFLICT (name) DO NOTHING;
