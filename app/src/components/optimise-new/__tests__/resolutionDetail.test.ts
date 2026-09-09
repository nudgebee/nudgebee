import {
  buildAppliedChanges,
  describeResolutionReference,
  formatDuration,
  formatQuantity,
  formatResolutionType,
  parseQuantity,
} from '../resolutionDetail';

// The two shapes this joins: what a resolution stores as applied (container →
// { cpu, memory }, memory in raw bytes) and what the recommendation carries as
// the before-state (container → [{ resource, allocated, recommended }]).
const APPLIED = {
  'workflow-server': { cpu: { request: '81m' }, memory: { request: 402258739, limit: 402258739 } },
};

const RECOMMENDATION = {
  'workflow-server': [
    { resource: 'cpu', allocated: { request: 0.3 }, recommended: { request: 0.081 } },
    { resource: 'memory', allocated: { request: 1073741824, limit: 1610612736 }, recommended: { request: 402258739 } },
  ],
};

describe('formatQuantity', () => {
  it('formats raw byte counts as the rest of the product does', () => {
    expect(formatQuantity(402258739, 'memory')).toBe('384 Mi');
    expect(formatQuantity(1073741824, 'memory')).toBe('1.0 Gi');
  });

  it('formats fractional cores as millicores', () => {
    expect(formatQuantity(0.081, 'cpu')).toBe('81m');
    expect(formatQuantity(2, 'cpu')).toBe('2.00');
  });

  it('passes through a quantity that already carries its unit', () => {
    // Re-deriving '81m' as though it were 81 cores is how a panel starts lying.
    expect(formatQuantity('81m', 'cpu')).toBe('81m');
    expect(formatQuantity('512Mi', 'memory')).toBe('512Mi');
  });

  it('formats a numeric string by its kind rather than echoing it', () => {
    expect(formatQuantity('402258739', 'memory')).toBe('384 Mi');
  });

  it('renders an absent value as a dash rather than zero', () => {
    expect(formatQuantity(null, 'cpu')).toBe('—');
    expect(formatQuantity(undefined, 'memory')).toBe('—');
    expect(formatQuantity('', 'cpu')).toBe('—');
  });
});

describe('parseQuantity', () => {
  it('reads the Kubernetes suffixes the payload actually uses', () => {
    expect(parseQuantity('81m')).toBeCloseTo(0.081);
    expect(parseQuantity('512Mi')).toBe(512 * 1024 * 1024);
    expect(parseQuantity('1.5')).toBe(1.5);
    expect(parseQuantity(0.3)).toBe(0.3);
  });

  it('refuses a suffix it does not know rather than guessing a magnitude', () => {
    // A wrong percentage is worse than an absent one.
    expect(parseQuantity('12xyz')).toBeNull();
    expect(parseQuantity('lots')).toBeNull();
    expect(parseQuantity(null)).toBeNull();
  });
});

describe('buildAppliedChanges', () => {
  it('reads an applied spec as a change, joining the recommendation for the before', () => {
    const [change] = buildAppliedChanges(APPLIED, RECOMMENDATION);

    expect(change.containerName).toBe('workflow-server');
    // Numeric, so the shared ResourceChangeCell renders and compares them exactly
    // as the recommendation panel does — CPU in cores, memory in bytes.
    expect(change.rows).toEqual([
      { label: 'CPU request', isMem: false, before: 0.3, after: 0.081, afterText: '81m' },
      { label: 'Memory request', isMem: true, before: 1073741824, after: 402258739, afterText: '384 Mi' },
      { label: 'Memory limit', isMem: true, before: 1610612736, after: 402258739, afterText: '384 Mi' },
    ]);
  });

  it('still shows the applied value in readable units when there is no before', () => {
    const [change] = buildAppliedChanges(APPLIED);

    expect(change.rows.map((r) => [r.label, r.before, r.afterText])).toEqual([
      ['CPU request', null, '81m'],
      ['Memory request', null, '384 Mi'],
      ['Memory limit', null, '384 Mi'],
    ]);
  });

  it('matches the older single-container payload shape onto a named container', () => {
    // The legacy recommendation shape collapses to one synthetic "default" row,
    // which still describes the only container the resolution touched.
    const legacy = { notifications: [{ resource: 'cpu', allocated: { request: 0.3 }, recommended: { request: 0.081 } }] };
    const [change] = buildAppliedChanges(APPLIED, legacy);

    expect(change.rows[0]).toEqual({ label: 'CPU request', isMem: false, before: 0.3, after: 0.081, afterText: '81m' });
  });

  it('does not borrow a before-state from an unrelated container', () => {
    const other = { 'some-other-container': [{ resource: 'cpu', allocated: { request: 4 }, recommended: { request: 1 } }] };
    const twoContainers = { ...APPLIED, api: { cpu: { request: '50m' } } };
    const changes = buildAppliedChanges(twoContainers, { ...other, api: [] });

    // Two candidates in the map means no sole fallback, and neither name matches.
    expect(changes.every((c) => c.rows.every((r) => r.before === null))).toBe(true);
  });

  it('returns nothing for a payload that is not a container spec', () => {
    expect(buildAppliedChanges(null)).toEqual([]);
    expect(buildAppliedChanges('a string')).toEqual([]);
    expect(buildAppliedChanges([{ message: 'a config finding' }])).toEqual([]);
    expect(buildAppliedChanges({ some: { unrelated: 'shape' } })).toEqual([]);
  });

  it('keeps the written value when the quantity cannot be parsed', () => {
    const [change] = buildAppliedChanges({ web: { cpu: { request: 'auto' } } }, { web: [{ resource: 'cpu', allocated: { request: 0.3 } }] });

    // Nothing to compare, so the panel falls back to the text rather than a dash.
    expect(change.rows[0].after).toBeNull();
    expect(change.rows[0].afterText).toBe('auto');
  });

  it('omits a field the resolution did not set', () => {
    const [change] = buildAppliedChanges({ web: { cpu: { request: '10m' } } }, {});

    expect(change.rows).toHaveLength(1);
    expect(change.rows[0].label).toBe('CPU request');
  });
});

describe('formatDuration', () => {
  it('reports coarse durations across the ranges that matter', () => {
    expect(formatDuration('2026-08-22T10:00:00Z', '2026-08-22T10:00:42Z')).toBe('42s');
    expect(formatDuration('2026-08-22T10:00:00Z', '2026-08-22T10:15:00Z')).toBe('15m');
    expect(formatDuration('2026-08-22T10:00:00Z', '2026-08-22T13:30:00Z')).toBe('3h 30m');
    expect(formatDuration('2026-08-20T10:00:00Z', '2026-08-22T16:00:00Z')).toBe('2d 6h');
  });

  it('returns null rather than NaN when an end is missing or unparseable', () => {
    expect(formatDuration('2026-08-22T10:00:00Z', null)).toBeNull();
    expect(formatDuration(undefined, '2026-08-22T10:00:00Z')).toBeNull();
    expect(formatDuration('not a date', '2026-08-22T10:00:00Z')).toBeNull();
  });

  it('returns null when the clocks disagree rather than printing a negative age', () => {
    expect(formatDuration('2026-08-22T10:00:00Z', '2026-08-22T09:00:00Z')).toBeNull();
  });
});

describe('describeResolutionReference', () => {
  // One column, five meanings — these are the shapes dev actually holds.
  it('pulls the number out of a pull-request URL', () => {
    expect(describeResolutionReference('https://github.com/nudgebee/nudgebee-infra/pull/950', 'Success')).toEqual({
      detail: '#950',
      href: 'https://github.com/nudgebee/nudgebee-infra/pull/950',
      missing: false,
      platform: 'github',
    });
  });

  it('reads a GitLab merge request by its own marker', () => {
    expect(describeResolutionReference('https://gitlab.com/x/y/-/merge_requests/12', 'Success').detail).toBe('!12');
  });

  it('falls back to the last path segment for any other link', () => {
    // A Jira browse URL ends in the key, which is the name a reader knows.
    expect(describeResolutionReference('https://acme.atlassian.net/browse/NB-1234', 'Success').detail).toBe('NB-1234');
    expect(describeResolutionReference('https://example.com/a/b/?x=1#frag', 'Success').detail).toBe('b');
  });

  it('treats a bare number as an id in someone else s system', () => {
    expect(describeResolutionReference('35838', 'InProgress')).toEqual({ detail: '#35838', href: '', missing: false, platform: null });
  });

  it('keeps a named resource, which is the one non-link reference worth reading', () => {
    expect(describeResolutionReference('nudgebee-templates-request-errors-alarm', 'Success').detail).toBe('nudgebee-templates-request-errors-alarm');
  });

  it('reports no detail for an internal id nobody can act on', () => {
    // "DeploymentChange 9c67786b-…" is noise wearing the costume of information.
    expect(describeResolutionReference('9c67786b-e65e-4efd-b804-ad3e4b9b968b', 'Success').detail).toBeNull();
  });

  it('reports no detail for the CLI marker, which is not a reference', () => {
    expect(describeResolutionReference('cli_execution', 'Failed').detail).toBeNull();
  });

  it('flags nothing-created only when the attempt failed', () => {
    expect(describeResolutionReference('', 'Failed').missing).toBe(true);
    expect(describeResolutionReference('', 'Success').missing).toBe(false);
    expect(describeResolutionReference(null, 'InProgress').missing).toBe(false);
    // The CLI marker is an absence of reference, not an absence of artefact.
    expect(describeResolutionReference('cli_execution', 'Failed').missing).toBe(false);
  });
});

describe('describeResolutionReference platform', () => {
  it('reads the platform off the link host, which is where it is actually known', () => {
    expect(describeResolutionReference('https://github.com/a/b/pull/1', 'Success').platform).toBe('github');
    expect(describeResolutionReference('https://gitlab.com/a/b/-/merge_requests/1', 'Success').platform).toBe('gitlab');
    expect(describeResolutionReference('https://bitbucket.org/a/b/pull-requests/1', 'Success').platform).toBe('bitbucket');
  });

  it('claims no platform for a self-hosted or unrecognised host', () => {
    expect(describeResolutionReference('https://git.internal.example/a/b/pull/1', 'Success').platform).toBeNull();
  });

  it('claims no platform for a ticket, which carries no provider at all', () => {
    // A bare id and a null provider_config — guessing an icon here would be wrong.
    expect(describeResolutionReference('35838', 'InProgress').platform).toBeNull();
  });
});

describe('describeResolutionReference ticket key', () => {
  it('prefers the key a person quotes over the internal id', () => {
    // ticket_resolution.go writes both; only the id reaches type_reference_id.
    expect(describeResolutionReference('35838', 'InProgress', 'NB-1234').detail).toBe('NB-1234');
  });

  it('falls back to the id when no key was recorded', () => {
    expect(describeResolutionReference('35838', 'InProgress', '').detail).toBe('#35838');
    expect(describeResolutionReference('35838', 'InProgress', undefined).detail).toBe('#35838');
  });

  it('does not let a key invent a platform', () => {
    // "NB-1234" looks Jira-shaped; nothing on the row actually says so.
    expect(describeResolutionReference('35838', 'InProgress', 'NB-1234').platform).toBeNull();
  });

  it('leaves a linked reference alone — the URL is the better source', () => {
    expect(describeResolutionReference('https://github.com/a/b/pull/7', 'Success', 'NB-1234').detail).toBe('#7');
  });
});

describe('formatResolutionType', () => {
  it('splits the PascalCase the backend writes', () => {
    expect(formatResolutionType('PullRequest')).toBe('Pull Request');
    expect(formatResolutionType('DeploymentChange')).toBe('Deployment Change');
    expect(formatResolutionType('CloudResource')).toBe('Cloud Resource');
  });

  it('leaves a single word alone', () => {
    expect(formatResolutionType('Ticket')).toBe('Ticket');
  });

  it('splits on separators too, since not every value is PascalCase', () => {
    expect(formatResolutionType('cli_execution')).toBe('cli execution');
  });

  it('returns an empty string for anything that is not a string', () => {
    // The value comes off an API row, so a falsy fallback alone is not enough:
    // a truthy non-string would crash on .trim().
    expect(formatResolutionType('')).toBe('');
    expect(formatResolutionType(null)).toBe('');
    expect(formatResolutionType(undefined)).toBe('');
    expect(formatResolutionType(42)).toBe('');
    expect(formatResolutionType({ type: 'PullRequest' })).toBe('');
  });
});
