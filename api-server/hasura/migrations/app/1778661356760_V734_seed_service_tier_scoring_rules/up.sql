-- V734_seed_service_tier_scoring_rules up.sql
--
-- Replaces the hardcoded tier-pattern scoring removed from
-- api-server/services/triage/scoring.go in the same change.
--
-- Each row is a system rule (tenant_id = NULL, account_id = NULL) with
-- rule_type = 'scoring', action = 'adjust_score'. is_editable = true and
-- can_override = true so tenants can edit, disable, or override per-tenant
-- via the existing triage rules UI.
--
-- Seed set is intentionally conservative: only patterns that are
-- almost universally unambiguous. Operators can author additional rules
-- per-tenant for their own architecture. We do NOT seed names that
-- collide with common product/code names (envoy as sidecar, loki as a
-- name, victoria, tempo, cortex, mimir, thanos, etc.).
--
-- Pattern semantics: match_service uses Go regexp.MatchString (unanchored),
-- so `(?i)<pattern>` mirrors the previous strings.Contains(strings.ToLower(name), pattern).
--
-- Score adjustments (mirror of (4 - tier) * 10):
--   Tier 0 (customer-facing) -> +40
--   Tier 1 (core infrastructure) -> +30
--   Tier 3 (monitoring)         -> +10
--   Tier 2 (default)            -> no rule (implicit 0)
--
-- UUID convention for stable, idempotent re-runs:
--   00000000-0000-0000-0000-0000001000XX  Tier 0 (service name)
--   00000000-0000-0000-0000-0000001100XX  Tier 1 (service name)
--   00000000-0000-0000-0000-0000001300XX  Tier 3 (service name)

INSERT INTO event_triage_rules (
  id, tenant_id, account_id, rule_type, action, action_value,
  match_service,
  priority, enabled, is_editable, can_override,
  name, description, reason,
  created_at, updated_at
) VALUES
  -- Tier 0: customer-facing (+40)
  ('00000000-0000-0000-0000-000000100001', NULL, NULL, 'scoring', 'adjust_score', '{"adjustment": 40, "reason": "Tier 0 (customer-facing) — system default"}'::jsonb, '(?i)ingress',          100, true, true, true, 'Service Tier 0: ingress',          'System default tier-based scoring. Editable per-tenant.', 'Customer-facing ingress controller', NOW(), NOW()),
  ('00000000-0000-0000-0000-000000100002', NULL, NULL, 'scoring', 'adjust_score', '{"adjustment": 40, "reason": "Tier 0 (customer-facing) — system default"}'::jsonb, '(?i)gateway',          100, true, true, true, 'Service Tier 0: gateway',          'System default tier-based scoring. Editable per-tenant.', 'Customer-facing API gateway',        NOW(), NOW()),
  ('00000000-0000-0000-0000-000000100003', NULL, NULL, 'scoring', 'adjust_score', '{"adjustment": 40, "reason": "Tier 0 (customer-facing) — system default"}'::jsonb, '(?i)nginx-controller', 100, true, true, true, 'Service Tier 0: nginx-controller', 'System default tier-based scoring. Editable per-tenant.', 'Customer-facing nginx controller',   NOW(), NOW()),

  -- Tier 1: core infrastructure (+30)
  ('00000000-0000-0000-0000-000000110001', NULL, NULL, 'scoring', 'adjust_score', '{"adjustment": 30, "reason": "Tier 1 (core infrastructure) — system default"}'::jsonb, '(?i)postgres',      100, true, true, true, 'Service Tier 1: postgres',      'System default tier-based scoring. Editable per-tenant.', 'Core database',             NOW(), NOW()),
  ('00000000-0000-0000-0000-000000110002', NULL, NULL, 'scoring', 'adjust_score', '{"adjustment": 30, "reason": "Tier 1 (core infrastructure) — system default"}'::jsonb, '(?i)mysql',         100, true, true, true, 'Service Tier 1: mysql',         'System default tier-based scoring. Editable per-tenant.', 'Core database',             NOW(), NOW()),
  ('00000000-0000-0000-0000-000000110003', NULL, NULL, 'scoring', 'adjust_score', '{"adjustment": 30, "reason": "Tier 1 (core infrastructure) — system default"}'::jsonb, '(?i)mongodb',       100, true, true, true, 'Service Tier 1: mongodb',       'System default tier-based scoring. Editable per-tenant.', 'Core database',             NOW(), NOW()),
  ('00000000-0000-0000-0000-000000110004', NULL, NULL, 'scoring', 'adjust_score', '{"adjustment": 30, "reason": "Tier 1 (core infrastructure) — system default"}'::jsonb, '(?i)mariadb',       100, true, true, true, 'Service Tier 1: mariadb',       'System default tier-based scoring. Editable per-tenant.', 'Core database',             NOW(), NOW()),
  ('00000000-0000-0000-0000-000000110005', NULL, NULL, 'scoring', 'adjust_score', '{"adjustment": 30, "reason": "Tier 1 (core infrastructure) — system default"}'::jsonb, '(?i)cockroach',     100, true, true, true, 'Service Tier 1: cockroach',     'System default tier-based scoring. Editable per-tenant.', 'Core database',             NOW(), NOW()),
  ('00000000-0000-0000-0000-000000110006', NULL, NULL, 'scoring', 'adjust_score', '{"adjustment": 30, "reason": "Tier 1 (core infrastructure) — system default"}'::jsonb, '(?i)cassandra',     100, true, true, true, 'Service Tier 1: cassandra',     'System default tier-based scoring. Editable per-tenant.', 'Core database',             NOW(), NOW()),
  ('00000000-0000-0000-0000-000000110007', NULL, NULL, 'scoring', 'adjust_score', '{"adjustment": 30, "reason": "Tier 1 (core infrastructure) — system default"}'::jsonb, '(?i)clickhouse',    100, true, true, true, 'Service Tier 1: clickhouse',    'System default tier-based scoring. Editable per-tenant.', 'Core analytics database',   NOW(), NOW()),
  ('00000000-0000-0000-0000-000000110008', NULL, NULL, 'scoring', 'adjust_score', '{"adjustment": 30, "reason": "Tier 1 (core infrastructure) — system default"}'::jsonb, '(?i)elasticsearch', 100, true, true, true, 'Service Tier 1: elasticsearch', 'System default tier-based scoring. Editable per-tenant.', 'Core search index',         NOW(), NOW()),
  ('00000000-0000-0000-0000-000000110009', NULL, NULL, 'scoring', 'adjust_score', '{"adjustment": 30, "reason": "Tier 1 (core infrastructure) — system default"}'::jsonb, '(?i)redis',         100, true, true, true, 'Service Tier 1: redis',         'System default tier-based scoring. Editable per-tenant.', 'Core cache',                NOW(), NOW()),
  ('00000000-0000-0000-0000-000000110010', NULL, NULL, 'scoring', 'adjust_score', '{"adjustment": 30, "reason": "Tier 1 (core infrastructure) — system default"}'::jsonb, '(?i)memcached',     100, true, true, true, 'Service Tier 1: memcached',     'System default tier-based scoring. Editable per-tenant.', 'Core cache',                NOW(), NOW()),
  ('00000000-0000-0000-0000-000000110011', NULL, NULL, 'scoring', 'adjust_score', '{"adjustment": 30, "reason": "Tier 1 (core infrastructure) — system default"}'::jsonb, '(?i)rabbitmq',      100, true, true, true, 'Service Tier 1: rabbitmq',      'System default tier-based scoring. Editable per-tenant.', 'Core message queue',        NOW(), NOW()),
  ('00000000-0000-0000-0000-000000110012', NULL, NULL, 'scoring', 'adjust_score', '{"adjustment": 30, "reason": "Tier 1 (core infrastructure) — system default"}'::jsonb, '(?i)kafka',         100, true, true, true, 'Service Tier 1: kafka',         'System default tier-based scoring. Editable per-tenant.', 'Core message queue',        NOW(), NOW()),
  ('00000000-0000-0000-0000-000000110013', NULL, NULL, 'scoring', 'adjust_score', '{"adjustment": 30, "reason": "Tier 1 (core infrastructure) — system default"}'::jsonb, '(?i)etcd',          100, true, true, true, 'Service Tier 1: etcd',          'System default tier-based scoring. Editable per-tenant.', 'Core key-value store',      NOW(), NOW()),
  ('00000000-0000-0000-0000-000000110014', NULL, NULL, 'scoring', 'adjust_score', '{"adjustment": 30, "reason": "Tier 1 (core infrastructure) — system default"}'::jsonb, '(?i)zookeeper',     100, true, true, true, 'Service Tier 1: zookeeper',     'System default tier-based scoring. Editable per-tenant.', 'Core coordination service', NOW(), NOW()),

  -- Tier 3: monitoring / observability (+10) — only universally unambiguous names
  ('00000000-0000-0000-0000-000000130001', NULL, NULL, 'scoring', 'adjust_score', '{"adjustment": 10, "reason": "Tier 3 (monitoring) — system default"}'::jsonb, '(?i)prometheus', 100, true, true, true, 'Service Tier 3: prometheus', 'System default tier-based scoring. Editable per-tenant.', 'Observability stack', NOW(), NOW()),
  ('00000000-0000-0000-0000-000000130002', NULL, NULL, 'scoring', 'adjust_score', '{"adjustment": 10, "reason": "Tier 3 (monitoring) — system default"}'::jsonb, '(?i)grafana',    100, true, true, true, 'Service Tier 3: grafana',    'System default tier-based scoring. Editable per-tenant.', 'Observability stack', NOW(), NOW())

ON CONFLICT (id) DO NOTHING;
