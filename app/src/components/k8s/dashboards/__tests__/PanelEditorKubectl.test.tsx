import React from 'react';
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import PanelEditorModal from '../PanelEditorModal';
import type { AccountOption, Panel } from '@api1/dashboards';

/*
 * A kubectl panel runs against an account's own AGENT, so only a Kubernetes
 * account can serve it. The editor has to say so while the panel is being
 * authored — a cloud scope saved here is a panel that can only fail at render,
 * and the backend refuses it on the way in anyway.
 */

jest.mock('../PanelPreview', () => ({
  __esModule: true,
  default: () => <div data-testid='panel-preview' />,
  PREVIEW_RAIL_WIDTH: 400,
  usePreviewRange: () => ({ start: 0, end: 1 }),
}));

jest.mock('../PanelProviderRow', () => ({ __esModule: true, default: () => null }));

jest.mock('../panelProviders', () => ({
  ...jest.requireActual('../panelProviders'),
  usePanelProviders: () => ({ loading: false, entries: [], total: 0 }),
  useEsIndexes: () => ({ loading: false, indexes: [] }),
}));

jest.mock('../panelAccess', () => ({
  missingDatasourceGrant: () => null,
  grantTooltip: () => '',
  queryableTables: (tables: unknown[]) => tables,
}));

jest.mock('@hooks/useTenantBranding', () => ({
  useBrandingConfig: () => ({ title: 'Brand' }),
  fillBrandTokens: (s: string) => s,
}));

// One of each kind: `kind` is what says whether an account manages a cluster.
const accounts = [
  { value: 'acc-k8s', label: 'k8s-dev', cloud_provider: 'K8s', kind: 'kubernetes' },
  { value: 'acc-aws', label: 'aws-prod', cloud_provider: 'AWS', kind: 'cloud' },
] as AccountOption[];

const kubectlPanel = (over: Partial<Panel> = {}): Panel =>
  ({
    id: 1,
    title: 'Pods',
    type: 'table',
    datasource: 'kubectl',
    account_type: 'K8s',
    account_ids: [],
    grid_pos: { x: 0, y: 0, w: 6, h: 8 },
    targets: [{ ref_id: 'A', expr: 'get pods -n kube-system' }],
    ...over,
  } as Panel);

const renderEditor = (p: Panel) => {
  const onSave = jest.fn();
  render(<PanelEditorModal open panel={p} isEdit accountOptions={accounts} onClose={jest.fn()} onSave={onSave} />);
  return onSave;
};

/**
 * Opens a ds/Select by its trigger id and returns the open listbox. The modal
 * renders in a portal, so the trigger is looked up on the document rather than
 * in the render container.
 */
const openSelect = (id: string): HTMLElement => {
  const trigger = document.querySelector(`#${id}`) as HTMLElement;
  expect(trigger).toBeTruthy();
  fireEvent.click(trigger);
  // The popup opens in its own portal, which leaves the dialog behind it
  // `aria-hidden` — so the listbox is only reachable as a hidden element. A
  // popup that has been used stays mounted, so the one just opened is the last.
  const listboxes = screen.getAllByRole('listbox', { hidden: true });
  return listboxes[listboxes.length - 1];
};

/** Picks an option out of an open ds/Select listbox. */
const choose = (listbox: HTMLElement, label: string) => fireEvent.click(within(listbox).getByText(label));

describe('PanelEditorModal — kubectl panel', () => {
  it('offers only Kubernetes accounts as a scope', () => {
    renderEditor(kubectlPanel());

    const listbox = openSelect('panel-account-type-select');
    expect(within(listbox).getByText('K8s')).toBeTruthy();
    // The cloud account has no agent to run kubectl, so it is not a scope the
    // author can pick at all.
    expect(within(listbox).queryByText('AWS')).toBeNull();
  });

  it('spells out what the command may be, and that the binary is added by the server', () => {
    renderEditor(kubectlPanel());

    const command = screen.getByPlaceholderText('get pods -n kube-system') as HTMLInputElement;
    expect(command.value).toBe('get pods -n kube-system');
    expect(document.body.textContent).toContain('get, describe, logs, top');
    expect(document.body.textContent).toContain('read flags only');
    expect(document.body.textContent).toContain('No secrets, no exec or port-forward, no file or template paths');
    expect(document.body.textContent).toContain('`kubectl` itself is added by the server');
  });
});

describe('PanelEditorModal — switching to kubectl', () => {
  it('drops a cloud scope the new datasource cannot serve', async () => {
    const onSave = renderEditor({
      id: 2,
      title: 'Spend',
      type: 'timeseries',
      datasource: 'metrics',
      account_type: 'AWS',
      account_ids: [],
      grid_pos: { x: 0, y: 0, w: 6, h: 8 },
      targets: [{ ref_id: 'A', expr: 'up' }],
    } as Panel);

    choose(openSelect('panel-datasource-select'), 'kubectl');

    // The AWS scope the panel arrived with cannot run kubectl, so the editor
    // lands on the one provider that can rather than saving a dead panel.
    const listbox = openSelect('panel-account-type-select');
    expect(within(listbox).queryByText('AWS')).toBeNull();
    fireEvent.keyDown(listbox, { key: 'Escape' });

    fireEvent.change(screen.getByPlaceholderText('get pods -n kube-system'), { target: { value: 'get pods -n prod' } });
    await act(async () => {
      fireEvent.click(screen.getByTestId('panel-save-btn'));
    });

    const saved: Panel = onSave.mock.calls[0][0];
    expect(saved.datasource).toBe('kubectl');
    expect(saved.account_type).toBe('K8s');
    // A snapshot of text has nothing to plot — the backend refuses any other
    // visualisation for a command datasource.
    expect(saved.type).toBe('table');
    expect(saved.targets?.[0].expr).toBe('get pods -n prod');
  });
});
