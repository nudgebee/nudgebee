-- Register the self-hosted SolarWinds Platform (Orion) metrics integration (#41941).
-- Separate from the SaaS 'solarwinds' integration: this one reads SWQL over SWIS,
-- and serves hosts and network devices only.
INSERT INTO integration_types(name, category, description) VALUES
  ('orion', 'metrics', 'SolarWinds Platform (Orion)')
ON CONFLICT (name) DO NOTHING;
