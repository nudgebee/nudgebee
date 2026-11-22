import React from 'react';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import DashboardView from '../DashboardView';
import type { Dashboard } from '@api1/dashboards';

/*
 * A new dashboard is nothing but browser state until Save, and every way off the
 * screen has to ask first. The side nav's "Application Grouping" is
 * /dashboards#groups — from a draft at /dashboards that is a hash change, which
 * Next announces as hashChangeStart rather than routeChangeStart, and the page
 * swapped the draft out without asking.
 */

type Handler = (url: string) => void;
const handlers: Record<string, Handler[]> = {};
const push = jest.fn();

// The router's own emitter, close enough: handlers run in order and a throw
// propagates to whoever emitted, which is how a handler cancels a transition.
const events = {
  on: (name: string, h: Handler) => {
    (handlers[name] ||= []).push(h);
  },
  off: (name: string, h: Handler) => {
    handlers[name] = (handlers[name] || []).filter((x) => x !== h);
  },
  emit: (name: string, url = '') => (handlers[name] || []).forEach((h) => h(url)),
};

jest.mock('next/router', () => ({
  useRouter: () => ({ events, push, replace: jest.fn(), asPath: '/dashboards', query: {} }),
}));

jest.mock('@api1/observability', () => ({ __esModule: true, default: { metricsQuery: jest.fn(), fetchLogs: jest.fn() } }));

jest.mock('@api1/dashboards', () => ({
  __esModule: true,
  default: { updateDashboard: jest.fn(), createDashboard: jest.fn() },
  EMPTY_DEFINITION: { panels: [] },
  isCommandDatasource: () => false,
  isKubernetesOnlyDatasource: () => false,
  KUBERNETES_ACCOUNT_KIND: 'kubernetes',
}));

jest.mock('@ui/Chart', () => ({ __esModule: true, default: { Line: () => null, Bar: () => null } }));
jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => ({ __esModule: true, default: () => null }));

/** What CustomDashboards hands the view for Blank dashboard: no id, nothing in it. */
const draft = { id: '', title: '', description: '', definition: { panels: [] } } as unknown as Dashboard;

function renderDraft() {
  const onBack = jest.fn();
  render(<DashboardView dashboard={draft} accounts={[]} canEdit initialEditing onBack={onBack} onChange={jest.fn()} />);
  return { onBack };
}

const typeTitle = (text: string) =>
  fireEvent.change(document.getElementById('dashboard-title-input') as HTMLInputElement, { target: { value: text } });

/** Emits as the router would, and reports whether a handler stopped it. */
function navigate(name: 'hashChangeStart' | 'routeChangeStart', url: string): 'held' | 'went' {
  let outcome: 'held' | 'went' = 'went';
  act(() => {
    try {
      events.emit(name, url);
    } catch {
      outcome = 'held';
    }
  });
  return outcome;
}

describe('leaving a new, unsaved dashboard', () => {
  beforeEach(() => {
    push.mockReset();
    for (const k of Object.keys(handlers)) delete handlers[k];
  });
  afterEach(cleanup);

  it('asks before a hash-only change such as Application Grouping', () => {
    renderDraft();
    typeTitle('Capacity review');

    expect(navigate('hashChangeStart', '/dashboards#groups')).toBe('held');
    expect(screen.getByText('Unsaved changes')).toBeInTheDocument();
  });

  it('drops the draft and goes where the viewer was headed on Discard', () => {
    const { onBack } = renderDraft();
    typeTitle('Capacity review');
    navigate('hashChangeStart', '/dashboards#groups');

    fireEvent.click(document.getElementById('dashboard-exit-discard-btn') as HTMLElement);

    expect(onBack).toHaveBeenCalledTimes(1);
    expect(push).toHaveBeenCalledWith('/dashboards#groups');
    // The navigation Discard starts is let through, once.
    expect(navigate('hashChangeStart', '/dashboards#groups')).toBe('went');
  });

  it('stays put on Cancel', () => {
    const { onBack } = renderDraft();
    typeTitle('Capacity review');
    navigate('hashChangeStart', '/dashboards#groups');

    fireEvent.click(document.getElementById('dashboard-exit-cancel-btn') as HTMLElement);

    expect(onBack).not.toHaveBeenCalled();
    expect(push).not.toHaveBeenCalled();
    expect((document.getElementById('dashboard-title-input') as HTMLInputElement).value).toBe('Capacity review');
  });

  it('still asks before a full route change', () => {
    renderDraft();
    typeTitle('Capacity review');

    expect(navigate('routeChangeStart', '/troubleshoot')).toBe('held');
    expect(screen.getByText('Unsaved changes')).toBeInTheDocument();
  });

  it('lets a hash change through when nothing has been typed', () => {
    renderDraft();

    expect(navigate('hashChangeStart', '/dashboards#groups')).toBe('went');
    expect(screen.queryByText('Unsaved changes')).not.toBeInTheDocument();
  });
});
