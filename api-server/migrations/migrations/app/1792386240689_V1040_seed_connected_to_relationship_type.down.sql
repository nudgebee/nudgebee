-- Fails on the foreign keys once a manual dependency or an edge uses
-- CONNECTED_TO: delete those (kg_delete_all_manual_dependencies) first.
DELETE FROM knowledge_graph_relationship_types WHERE name = 'CONNECTED_TO';
