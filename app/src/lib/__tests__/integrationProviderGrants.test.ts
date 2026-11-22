import fs from 'fs';
import path from 'path';
import {
  OBSERVABILITY_INTEGRATION_PROVIDERS,
  PROVIDER_GRANT_ACTIONS,
  USE_ONLY_INTEGRATION_TYPES,
  actionHasProviderGrant,
  integrationGrantLabel,
  isProviderGrantModule,
  providerGrantCatalogEntries,
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
    expect(integrationGrantLabel('integrations.es')).toBe('Elasticsearch');
    expect(integrationGrantLabel('integrations.datadog')).toBe('Datadog');
    expect(integrationGrantLabel('integrations')).toBeNull();
    // Jira has a use grant but no manage grant.
    expect(integrationGrantLabel('integrations.jira')).toBe('Jira');
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
      // A use grant is not a manage grant.
      expect(actionHasProviderGrant('integrations_create_config', ['integrations.datadog:Execute'])).toBe(false);
      expect(actionHasProviderGrant('integrations_create_config', ['integrations.jira:Write'])).toBe(false);
      // Account-bound grants are inert for integrations, as for the integrations module.
      expect(actionHasProviderGrant('integrations_create_config', ['scoped:integrations.datadog:Write'])).toBe(false);
      expect(actionHasProviderGrant('integrations_create_config', [])).toBe(false);
    });
  });

  describe('editor rows', () => {
    const entries = providerGrantCatalogEntries({ integrations_create_config: '  Create or update an integration  ' });
    const byModule = new Map(entries.map((e) => [e.module, e]));

    it('offers one row per integration type, on its own tab, sorted by name', () => {
      expect(entries).toHaveLength(OBSERVABILITY_INTEGRATION_PROVIDERS.length + USE_ONLY_INTEGRATION_TYPES.length);
      expect(new Set(entries.map((e) => e.module)).size).toBe(entries.length);
      const labels = entries.map((e) => integrationGrantLabel(e.module));
      expect(labels.every((l) => !!l)).toBe(true);
      expect(labels).toEqual([...labels].sort((a, b) => a!.localeCompare(b!)));
      for (const e of entries) expect(e.scope).toBe('provider');
    });

    it('keeps the manage (Write) grant for observability providers only, with integrations:Read', () => {
      for (const p of OBSERVABILITY_INTEGRATION_PROVIDERS) {
        const e = byModule.get(`integrations.${p.type}`)!;
        expect(e.classes).toEqual(['Read', 'Write', 'Execute']);
        expect(e.requires).toEqual({ Write: 'integrations:Read' });
        expect(e.actions.Write?.map((a) => a.name)).toEqual([...PROVIDER_GRANT_ACTIONS].sort());
      }
      expect(byModule.get('integrations.datadog')!.actions.Write?.find((a) => a.name === 'integrations_create_config')?.description).toBe(
        'Create or update an integration'
      );
      for (const t of USE_ONLY_INTEGRATION_TYPES) {
        const e = byModule.get(`integrations.${t.type}`)!;
        expect(e.classes).toEqual(['Read', 'Execute']);
        expect(e.requires).toBeUndefined();
        expect(e.actions).toEqual({});
      }
    });

    it('offers use (Read, Execute) for every type, explained, and uncoupled from manage', () => {
      for (const e of entries) {
        expect(e.classes).toEqual(expect.arrayContaining(['Read', 'Execute']));
        expect(e.independentClasses).toBe(true);
        // No RPC action backs a use box, so its tooltip is the note.
        expect(e.actions.Read).toBeUndefined();
        expect(e.actions.Execute).toBeUndefined();
        expect(e.notes?.Read).toMatch(/read through/);
        expect(e.notes?.Execute).toMatch(/make changes through/);
      }
      expect(byModule.get('integrations.mcp')!.notes?.Read).toContain('MCP servers');
    });

    it('labels every type, MCP as one row', () => {
      expect(integrationGrantLabel('integrations.mcp')).toBe('MCP servers');
      expect(integrationGrantLabel('integrations.postgresql')).toBe('PostgreSQL');
      expect(integrationGrantLabel('integrations.github')).toBe('GitHub');
      expect(integrationGrantLabel('integrations.datadog_webhook')).toBeNull();
    });
  });

  // A use row for a type no run checks is a box that does nothing; a type a run
  // checks without a row is a grant only SQL can give. These are the types the
  // runs resolve, read from the servers' source.
  describe('use rows cover every type a run checks', () => {
    const repo = path.join(process.cwd(), '..');
    const offered = new Set(providerGrantCatalogEntries().map((e) => e.module.slice('integrations.'.length)));
    const read = (rel: string) => fs.readFileSync(path.join(repo, rel), 'utf8');
    const goStrings = (src: string, varName: string) => {
      const m = src.match(new RegExp(`${varName}\\s*=\\s*\\[\\]string\\{([^}]*)\\}`));
      expect(m).toBeTruthy();
      return [...m![1].matchAll(/"([a-z0-9_]+)"/g)].map((x) => x[1]);
    };

    it('llm-server: integration- and ticket-configured tools', () => {
      const toolsDir = path.join(repo, 'llm/llm-server/tools');
      const types = fs
        .readdirSync(toolsDir)
        .filter((f) => f.endsWith('.go') && !f.endsWith('_test.go'))
        .flatMap((f) => [
          ...fs
            .readFileSync(path.join(toolsDir, f), 'utf8')
            .matchAll(/ConfigType:\s*"([a-z0-9_]+)",\s*\n\s*ConfigSource:\s*core\.ToolConfigSource(?:Integration|Ticket)\b/g),
        ])
        .map((m) => m[1]);
      expect(types.length).toBeGreaterThan(10);
      expect(types.filter((t) => !offered.has(t))).toEqual([]);
    });

    it('llm-server: the ticket tool that picks a type per call', () => {
      const types = goStrings(read('llm/llm-server/tools/core/tool_config.go'), 'TicketAllIntegrationTypes');
      expect(types.filter((t) => !offered.has(t))).toEqual([]);
    });

    it('runbook-server: ticket steps', () => {
      const types = goStrings(read('runbook-server/internal/tasks/tickets/params.go'), 'ticketPlatforms');
      expect(types.filter((t) => !offered.has(t))).toEqual([]);
    });

    it('MCP tools and steps (one type for every server)', () => {
      expect(offered.has('mcp')).toBe(true);
    });
  });
});
