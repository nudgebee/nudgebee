import fs from 'fs';
import path from 'path';
import {
  OBSERVABILITY_INTEGRATION_PROVIDERS,
  PROVIDER_GRANT_ACTIONS,
  actionHasProviderGrant,
  isProviderGrantModule,
  providerGrantCatalogEntries,
  providerGrantLabel,
  providerGrantModule,
} from '@lib/integrationProviderGrants';

// The Observability tab's provider ids, read from the source rather than
// imported: integration.jsx pulls in icons and React components this suite has
// no use for.
function observabilitySectionProviders(): string[] {
  const src = fs.readFileSync(path.join(process.cwd(), 'src/components/accounts/integration.jsx'), 'utf8');
  const block = src.slice(src.indexOf("id: 'observability'"));
  const list = block.slice(block.indexOf('providers: ['), block.indexOf(']'));
  return [...list.matchAll(/'([A-Z0-9_]+)'/g)].map((m) => m[1]);
}

describe('integrationProviderGrants', () => {
  it('offers a grant for exactly the providers on the Observability tab', () => {
    const sections = observabilitySectionProviders();
    expect(sections.length).toBeGreaterThan(10);
    expect(OBSERVABILITY_INTEGRATION_PROVIDERS.map((p) => p.section).sort()).toEqual([...sections].sort());
  });

  it('keys each grant on the stored type, lowercased', () => {
    expect(providerGrantModule('datadog')).toBe('integrations.datadog');
    // Elasticsearch is stored as "ES"; OTEL's stored type differs from its tab id.
    expect(providerGrantModule('ES')).toBe('integrations.es');
    expect(providerGrantModule('otel_clickhouse')).toBe('integrations.otel_clickhouse');
    expect(providerGrantModule('OTEL')).toBeNull();
    // Webhooks and non-observability types have no provider grant.
    expect(providerGrantModule('datadog_webhook')).toBeNull();
    expect(providerGrantModule('jira')).toBeNull();
    expect(providerGrantModule(undefined)).toBeNull();
  });

  it('labels a grant with the name the Integrations page uses', () => {
    expect(providerGrantLabel('integrations.es')).toBe('Elasticsearch');
    expect(providerGrantLabel('integrations.datadog')).toBe('Datadog');
    expect(providerGrantLabel('integrations')).toBeNull();
    expect(providerGrantLabel('integrations.jira')).toBeNull();
    expect(isProviderGrantModule('integrations.jira')).toBe(false);
  });

  describe('gateway admission', () => {
    it('admits the covered writes for a holder of any provider grant', () => {
      for (const action of PROVIDER_GRANT_ACTIONS) {
        expect(actionHasProviderGrant(action, ['integrations:Read', 'integrations.datadog:Write'])).toBe(true);
      }
    });

    it('admits nothing else', () => {
      const grants = ['integrations:Read', 'integrations.datadog:Write'];
      expect(actionHasProviderGrant('integrations_upsert_discovery_target', grants)).toBe(false);
      expect(actionHasProviderGrant('integrations_get_schema', grants)).toBe(false);
      expect(actionHasProviderGrant('integrations_create_config', ['integrations.datadog:Read'])).toBe(false);
      expect(actionHasProviderGrant('integrations_create_config', ['integrations.jira:Write'])).toBe(false);
      // Account-bound grants are inert for integrations, as for the integrations module.
      expect(actionHasProviderGrant('integrations_create_config', ['scoped:integrations.datadog:Write'])).toBe(false);
      expect(actionHasProviderGrant('integrations_create_config', [])).toBe(false);
    });
  });

  it('renders one Write-only editor row per provider that needs integrations:Read', () => {
    const entries = providerGrantCatalogEntries({ integrations_create_config: '  Create or update an integration  ' });
    expect(entries).toHaveLength(OBSERVABILITY_INTEGRATION_PROVIDERS.length);
    for (const e of entries) {
      expect(e.scope).toBe('provider');
      expect(e.classes).toEqual(['Write']);
      expect(e.requires).toBe('integrations:Read');
      expect(e.actions.Write?.map((a) => a.name)).toEqual([...PROVIDER_GRANT_ACTIONS].sort());
    }
    expect(entries[0].actions.Write?.find((a) => a.name === 'integrations_create_config')?.description).toBe('Create or update an integration');
  });
});
