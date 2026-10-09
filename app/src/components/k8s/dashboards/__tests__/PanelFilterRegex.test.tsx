import React from 'react';
import { cleanup, fireEvent, render, renderHook, screen, waitFor } from '@testing-library/react';
import EntityQueryBuilder from '../EntityQueryBuilder';
import { usePanelData } from '../usePanelData';
import { buildEntityQuery, defaultDraft, tablesFor, type EntityQueryDraft } from '../entityQuery';
import type { AccountOption, Panel } from '@api1/dashboards';

/*
 * "A or B" on one column of a panel filter.
 *
 * Rows are AND-ed, so the author's only OR is a regex — `central|edge` — and on
 * a traces panel two rows with the same column and operator used to run as just
 * the last one, without a word. The builder now offers the regex and hints at
 * its shape; the panel says so when a row was replaced.
 */

const traceV2 = jest.fn();

jest.mock('@api1/kubernetes/trace', () => ({
  __esModule: true,
  default: { traceV2: (...args: unknown[]) => traceV2(...args), traceGroupV2: jest.fn(async () => ({ traces_grouping_v3: [] })) },
}));

jest.mock('@api1/observability', () => ({
  __esModule: true,
  default: { metricsQuery: jest.fn(), fetchLogs: jest.fn() },
}));

jest.mock('@api1/dashboards', () => ({
  __esModule: true,
  default: {},
  isCommandDatasource: () => false,
}));

// One resolvable account, so the fetch effect gets past its scope guard.
jest.mock('../panelAccounts', () => ({
  resolvePanelAccounts: () => [{ value: 'acc-1', label: 'Account 1' }],
  panelQueryAccounts: () => ({ accounts: [{ value: 'acc-1', label: 'Account 1' }] }),
}));

// A native <select> stands in for the DS one: its own behaviour is its own tests' business.
jest.mock('@ui/Select', () => ({
  __esModule: true,
  Select: (props: { multiple?: boolean; value: unknown; options?: { label: string; value: string }[]; onChange: (v: unknown) => void }) => (
    <select
      data-testid={`select-${(props.options || [])[0]?.value}`}
      multiple={props.multiple}
      value={(props.multiple ? props.value || [] : props.value ?? '') as never}
      onChange={(e) => props.onChange(props.multiple ? [...e.target.selectedOptions].map((o) => o.value) : e.target.value)}
    >
      {(props.options || []).map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  ),
}));

const accounts = [{ value: 'acc-1', label: 'Account 1' }] as AccountOption[];

function tracesPanel(filters: EntityQueryDraft['filters']): Panel {
  return {
    id: 1,
    title: 'spans',
    type: 'table',
    datasource: 'traces',
    account_ids: ['acc-1'],
    grid_pos: { x: 0, y: 0, w: 6, h: 8 },
    targets: [{ ref_id: 'A', query: buildEntityQuery({ ...defaultDraft('traces_v2'), filters }), time_column: 'timestamp' }],
  } as unknown as Panel;
}

const renderPanel = (panel: Panel) =>
  renderHook(() => usePanelData({ panel, accounts, variables: {}, startTime: 0, endTime: 60 * 60 * 1000, immediate: true }));

describe('a traces panel with two filters on one column and operator', () => {
  beforeEach(() => {
    traceV2.mockReset();
    traceV2.mockResolvedValue({ traces_list: [] });
  });
  afterEach(cleanup);

  it('says only the last one ran, and how to combine them', async () => {
    const { result } = renderPanel(
      tracesPanel([
        { column: 'resource', operator: '_ilike', value: '%central%' },
        { column: 'resource', operator: '_ilike', value: '%edge%' },
      ])
    );
    await waitFor(() => expect(result.current.data).not.toBeNull());

    expect(result.current.warning).toContain('Only the last filter on resource ran');
    expect(result.current.warning).toContain('"matches regex" for patterns (a|b)');
  });

  it('shows why the server refused a pattern, instead of an empty panel', async () => {
    // The traces call used to hand back only `data`, so a refusal rendered as
    // "Nothing came back" — indistinguishable from a filter that matched nothing.
    traceV2.mockRejectedValue(new Error('regex escape \\d is not supported: write a class such as [0-9] instead'));
    const { result } = renderPanel(tracesPanel([{ column: 'resource', operator: '_regex', value: '\\d+' }]));
    await waitFor(() => expect(result.current.error).not.toBeNull());

    expect(JSON.stringify(result.current.error)).toContain('is not supported');
    expect(result.current.data).toBeNull();
  });

  it('runs one regex row as one filter, with nothing to warn about', async () => {
    const { result } = renderPanel(tracesPanel([{ column: 'resource', operator: '_regex', value: 'central|edge' }]));
    await waitFor(() => expect(result.current.data).not.toBeNull());

    expect(traceV2.mock.calls[0][0].where).toEqual({ resource: { _regex: 'central|edge' } });
    expect(result.current.warning).toBeNull();
  });
});

describe('the filter builder', () => {
  afterEach(cleanup);

  const draftWith = (operator: string): EntityQueryDraft => ({
    ...defaultDraft('traces_v2'),
    filters: [{ column: 'resource', operator, value: '' }],
  });

  it('offers a regex match on a text column and hints at the alternation', () => {
    render(<EntityQueryBuilder draft={draftWith('_regex')} tables={tablesFor('traces')} onChange={jest.fn()} />);

    const operator = screen.getByTestId('select-_eq') as HTMLSelectElement;
    expect([...operator.options].map((o) => o.label)).toEqual(expect.arrayContaining(['matches regex', 'does not match regex']));
    expect(screen.getByPlaceholderText('central|edge')).toBeTruthy();
  });

  it('keeps the plain hint for every other operator', () => {
    render(<EntityQueryBuilder draft={draftWith('_ilike')} tables={tablesFor('traces')} onChange={jest.fn()} />);
    expect(screen.queryByPlaceholderText('central|edge')).toBeNull();
    expect(screen.getByPlaceholderText('value or $namespace')).toBeTruthy();
  });

  it('stores the pattern under the regex operator when the author picks it', () => {
    const onChange = jest.fn();
    render(
      <EntityQueryBuilder
        draft={{ ...defaultDraft('traces_v2'), filters: [{ column: 'resource', operator: '_ilike', value: 'central|edge' }] }}
        tables={tablesFor('traces')}
        onChange={onChange}
      />
    );
    fireEvent.change(screen.getByTestId('select-_eq'), { target: { value: '_regex' } });

    const [, query] = onChange.mock.calls[0];
    expect(query.where).toEqual({ _and: [{ _binary: { resource: { _regex: 'central|edge' } } }] });
  });
});
