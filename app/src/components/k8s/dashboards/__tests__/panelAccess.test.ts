import { canQueryTable, grantTooltip, missingDatasourceGrant, missingPanelGrant, missingTableGrant, queryableTables } from '../panelAccess';
import { findTable, tablesFor } from '../entityQuery';
import { PANEL_TEMPLATES } from '../panelTemplates';
import { hasPermission, isGrantsOnlyUser } from '@lib/auth';

jest.mock('@lib/auth', () => ({
  hasPermission: jest.fn(),
  isGrantsOnlyUser: jest.fn(),
  missingPermissionMessage: (permission: string) => `You need the "${permission}" permission. Ask an admin to grant it.`,
}));

const mockGrantsOnly = isGrantsOnlyUser as jest.Mock;
const mockHasPermission = hasPermission as jest.Mock;

/** A viewer whose access comes purely from custom-role grants, holding `grants`. */
function grantsOnlyWith(...grants: string[]) {
  mockGrantsOnly.mockReturnValue(true);
  mockHasPermission.mockImplementation((module: string, cls: string) => grants.includes(`${module}:${cls}`));
}

/** Any user with a built-in role — tenant admin, account admin, namespace admin. */
function builtInRoleUser() {
  mockGrantsOnly.mockReturnValue(false);
  mockHasPermission.mockReturnValue(false);
}

beforeEach(() => {
  jest.clearAllMocks();
});

describe('missingDatasourceGrant', () => {
  it('names the permission each datasource read path is gated on', () => {
    // The classification of the action each datasource calls — metrics_list,
    // logs_list, traces_grouping_v3, dashboards_execute_(entity_)query — which
    // is what the in-app gateway checks. A wrong name here sends the author to
    // their admin for a grant that will not unblock the panel.
    grantsOnlyWith();
    expect(missingDatasourceGrant('metrics')).toBe('metrics:Read');
    expect(missingDatasourceGrant('logs')).toBe('logs:Read');
    expect(missingDatasourceGrant('traces')).toBe('traces:Read');
    expect(missingDatasourceGrant('redis')).toBe('dashboards:Execute');
    expect(missingDatasourceGrant('rabbitmq')).toBe('dashboards:Execute');
    expect(missingDatasourceGrant('postgresql')).toBe('dashboards:Execute');
    expect(missingDatasourceGrant('nudgebee')).toBe('dashboards:Execute');
  });

  it('clears a datasource the viewer holds the grant for', () => {
    grantsOnlyWith('metrics:Read', 'dashboards:Execute');
    expect(missingDatasourceGrant('metrics')).toBeUndefined();
    expect(missingDatasourceGrant('redis')).toBeUndefined();
    expect(missingDatasourceGrant('logs')).toBe('logs:Read');
  });

  it('never gates a text panel, which reads nothing', () => {
    grantsOnlyWith();
    expect(missingDatasourceGrant('text')).toBeUndefined();
  });

  it('gates nothing for a user with a built-in role', () => {
    builtInRoleUser();
    for (const datasource of ['metrics', 'logs', 'traces', 'redis', 'rabbitmq', 'postgresql', 'nudgebee']) {
      expect(missingDatasourceGrant(datasource)).toBeUndefined();
    }
  });
});

describe('missingTableGrant', () => {
  it('gates nothing for a user with a built-in role', () => {
    // The engine denies only a grants-only holder: a tenant admin reads
    // everything, and an account/namespace role lands in the account-restriction
    // branch rather than a denial. Greying tables out for them would be stricter
    // than the server.
    builtInRoleUser();
    expect(tablesFor('nudgebee').every(canQueryTable)).toBe(true);
  });

  it('names the grant a grants-only holder is missing', () => {
    grantsOnlyWith('events:Read');
    expect(missingTableGrant(findTable('events_v2'))).toBeUndefined();
    expect(missingTableGrant(findTable('event_groupings_v2'))).toBeUndefined();
    expect(missingTableGrant(findTable('spend_groupings_v2'))).toBe('spend:Read');
    expect(missingTableGrant(findTable('ticket_groupings_v2'))).toBe('tickets:Read');
  });

  it('accepts a Write grant, which implies Read in the engine', () => {
    // query/service.go admits `HasPermission(module,"Read") || ...("Write")`.
    // Demanding Read here would grey a table out for someone the server allows.
    grantsOnlyWith('tickets:Write');
    expect(missingTableGrant(findTable('ticket_groupings_v2'))).toBeUndefined();
  });

  it('never gates a traces table', () => {
    // Read through the traces service, not the query engine — the module on a
    // trace table is not what authorizes it.
    grantsOnlyWith();
    expect(tablesFor('traces').every(canQueryTable)).toBe(true);
  });
});

describe('queryableTables', () => {
  it('keeps only what the viewer may read, in order', () => {
    grantsOnlyWith('events:Read', 'recommendations:Read');
    expect(queryableTables(tablesFor('nudgebee')).map((t) => t.value)).toEqual([
      'events_v2',
      'event_groupings_v2',
      'recommendations_v2',
      'recommendation_groupings_v2',
      'recommendation_security_cis_groupings_v2',
      'recommendation_security_v2',
    ]);
  });
});

describe('missingPanelGrant', () => {
  it('reports the datasource grant before the table one', () => {
    // Both are missing here. `dashboards:Execute` is the one to ask for first —
    // without it no panel of any datasource renders, so naming the table module
    // would send the author back for a second grant straight after.
    grantsOnlyWith();
    const savings = PANEL_TEMPLATES.find((w) => w.id === 'savings-by-rule');
    expect(savings?.panel.datasource).toBe('nudgebee');
    expect(missingPanelGrant(savings!.panel)).toBe('dashboards:Execute');
  });

  it('reads the table off the stored query once the datasource clears', () => {
    grantsOnlyWith('dashboards:Execute', 'events:Read');
    const savings = PANEL_TEMPLATES.find((w) => w.id === 'savings-by-rule');
    expect(missingPanelGrant(savings!.panel)).toBe('recommendations:Read');

    const events = PANEL_TEMPLATES.find((w) => w.panel.datasource === 'nudgebee' && (w.panel.targets?.[0]?.query as any)?.table === 'events_v2');
    expect(missingPanelGrant(events!.panel)).toBeUndefined();
  });

  it('gates every other datasource on its own read path', () => {
    grantsOnlyWith('metrics:Read');
    const metric = PANEL_TEMPLATES.find((w) => w.panel.datasource === 'metrics');
    const trace = PANEL_TEMPLATES.find((w) => w.panel.datasource === 'traces');
    expect(missingPanelGrant(metric!.panel)).toBeUndefined();
    expect(missingPanelGrant(trace!.panel)).toBe('traces:Read');
    expect(missingPanelGrant({ datasource: 'logs' })).toBe('logs:Read');
    expect(missingPanelGrant({ datasource: 'postgresql' })).toBe('dashboards:Execute');
  });

  it('clears every widget for a user with a built-in role', () => {
    builtInRoleUser();
    for (const widget of PANEL_TEMPLATES) {
      expect(missingPanelGrant(widget.panel)).toBeUndefined();
    }
  });

  it('gates nothing on a panel with no query, or one naming an unknown table', () => {
    // findTable falls back to the first table for an unknown name, which would
    // otherwise report events:Read for a table that is not events.
    grantsOnlyWith('dashboards:Execute');
    expect(missingPanelGrant({ datasource: 'nudgebee', targets: [] })).toBeUndefined();
    expect(missingPanelGrant({ datasource: 'nudgebee', targets: [{ query: { table: 'not_a_table_v2' } }] })).toBeUndefined();
  });
});

describe('grantTooltip', () => {
  it('names one permission the way every other disabled control does', () => {
    expect(grantTooltip('tickets:Read')).toBe('You need the "tickets:Read" permission. Ask an admin to grant it.');
  });

  it('lists them all when a control stands for several panels', () => {
    expect(grantTooltip(['tickets:Read', 'spend:Read'])).toBe('You need these permissions: tickets:Read, spend:Read. Ask an admin to grant them.');
  });
});
