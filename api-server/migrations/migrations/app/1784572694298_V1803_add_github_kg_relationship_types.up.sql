INSERT INTO knowledge_graph_relationship_types (name, value)
VALUES
    ('MEMBER_OF', 'MEMBER_OF'),
    ('SAME_AS', 'SAME_AS')
ON CONFLICT (name) DO NOTHING;
