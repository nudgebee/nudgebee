-- Re-key knowledge-graph nodes whose primary key was not derived from their
-- natural key, so that id and (unique_key, cloud_account_id, tenant_id) identify
-- the same row for every row in the table.
--
-- Nine node-creation sites built DbNode literals by hand with ID: uuid.New(), so
-- the same logical node arrived with a fresh primary key on every build. The node
-- upsert's ON CONFLICT (id) arm never matched, Postgres fell through to a plain
-- INSERT, and the unique constraint on (unique_key, cloud_account_id, tenant_id)
-- rejected it with 23505 — aborting the single transaction that covers the whole
-- graph. Affected tenants stopped updating entirely and could not recover: the
-- tombstoning that would age the stale row out only runs after a successful save.
--
-- The code now derives every node id from its natural key via core.NodeIDFor,
-- which keeps ON CONFLICT (id) correct going forward. Rows written before that
-- still carry an underived id and would keep colliding, so they are rewritten here
-- to the id the code will derive: UUIDv5 over the OID namespace of
-- unique_key || tenant_id || cloud_account_id, matching NodeIDFor exactly.
--
-- Rewriting rather than removing: an underived id is not evidence that a live
-- source still emits the node. On dev, 18 of the 21 affected rows were seeded
-- tenant graphs that no build would have recreated, so discarding them would have
-- destroyed data this migration simply corrects.
--
-- Edge endpoints are repointed first, while the old ids still exist. The mapping
-- is materialised once because the derivation cannot use an index. Requires
-- pgcrypto for digest().

CREATE TEMP TABLE kg_node_id_rekey AS
WITH hashed AS (
    SELECT id AS old_id,
           digest(
               decode('6ba7b8129dad11d180b400c04fd430c8', 'hex') ||
               convert_to(unique_key || tenant_id::text || cloud_account_id::text, 'UTF8'),
               'sha1'
           ) AS d
    FROM knowledge_graph_node
)
SELECT old_id,
       (
           encode(substring(d FROM 1 FOR 4), 'hex') || '-' ||
           encode(substring(d FROM 5 FOR 2), 'hex') || '-' ||
           encode(set_byte(substring(d FROM 7 FOR 2), 0, (get_byte(d, 6) & 15) | 80), 'hex') || '-' ||
           encode(set_byte(substring(d FROM 9 FOR 2), 0, (get_byte(d, 8) & 63) | 128), 'hex') || '-' ||
           encode(substring(d FROM 11 FOR 6), 'hex')
       )::uuid AS new_id
FROM hashed;

DELETE FROM kg_node_id_rekey WHERE old_id = new_id;

CREATE INDEX ON kg_node_id_rekey (old_id);

UPDATE knowledge_graph_edge e
SET source_node_id = r.new_id
FROM kg_node_id_rekey r
WHERE e.source_node_id = r.old_id;

UPDATE knowledge_graph_edge e
SET destination_node_id = r.new_id
FROM kg_node_id_rekey r
WHERE e.destination_node_id = r.old_id;

UPDATE knowledge_graph_node n
SET id = r.new_id
FROM kg_node_id_rekey r
WHERE n.id = r.old_id;

DROP TABLE kg_node_id_rekey;
