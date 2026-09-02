-- Register the 'discovery' integration type so forager discovery datasources
-- (proxy type "discovery-proxy") can be auto-registered by the relay server.
-- Without this row the relay's INSERT INTO integrations fails the
-- integrations_type_fkey constraint and discovery datasources stay invisible
-- server-side.
INSERT INTO integration_types (name, category, description) VALUES
  ('discovery', 'server', 'Forager VM discovery (network sweep + package inventory)')
ON CONFLICT (name) DO NOTHING;
