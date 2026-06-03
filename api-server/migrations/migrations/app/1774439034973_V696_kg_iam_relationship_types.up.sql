
INSERT INTO knowledge_graph_relationship_types (name, value)
VALUES
    ('RUNS_AS', 'RUNS_AS'),
    ('ASSUMES', 'ASSUMES')
ON CONFLICT (name) DO NOTHING;
