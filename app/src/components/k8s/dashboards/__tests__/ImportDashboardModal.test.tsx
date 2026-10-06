import React from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import ImportDashboardModal from '../ImportDashboardModal';
import apiDashboards, { type AccountOption } from '@api1/dashboards';
import { snackbar } from '@ui/Toast';

/*
 * Uploading is a second way to fill the SAME editor, so what matters is that
 * the file's text lands there — and that anything which is not a .json file is
 * turned away before it does, since the picker's `accept` is only a hint.
 */

jest.mock('@api1/dashboards', () => ({ __esModule: true, default: { saveDashboard: jest.fn() } }));

jest.mock('@hooks/useTenantBranding', () => ({ useTenantBranding: () => ({ baseTitle: 'Brand' }) }));

jest.mock('@ui/Toast', () => ({ snackbar: { error: jest.fn(), success: jest.fn() } }));

jest.mock('@ui/CodeEditor', () => ({
  __esModule: true,
  CodeEditor: ({ value, onChange }: { value: string; onChange: (next: string) => void }) => (
    <textarea data-testid='json-editor' value={value} onChange={(e) => onChange(e.target.value)} />
  ),
}));

const accounts = [{ value: 'acc-1', label: 'k8s-dev', cloud_provider: 'K8s' }] as AccountOption[];

const DASHBOARD = JSON.stringify({ title: 'From a file', definition: { panels: [] } });

/** A file whose one panel is scoped to an account that exists here, so the modal prefills the mapping and Import is enabled. */
const IMPORTABLE = JSON.stringify({
  title: 'CPU dashboard',
  tags: ['cpu'],
  definition: {
    panels: [
      {
        id: 1,
        title: 'CPU used',
        type: 'timeseries',
        datasource: 'metrics',
        account_ids: ['acc-1'],
        grid_pos: { x: 0, y: 0, w: 12, h: 8 },
        targets: [{ ref_id: 'A', expr: 'up' }],
      },
    ],
  },
});

const renderModal = () => render(<ImportDashboardModal open accountOptions={accounts} onClose={jest.fn()} onDraft={jest.fn()} />);

const upload = (file: File) => {
  fireEvent.change(screen.getByTestId('import-json-file-input'), { target: { files: [file] } });
};

const editor = () => screen.getByTestId('json-editor') as HTMLTextAreaElement;

describe('ImportDashboardModal — file upload', () => {
  beforeEach(() => jest.clearAllMocks());

  it('loads a .json file into the editor and names it', async () => {
    renderModal();
    upload(new File([DASHBOARD], 'my-dashboard.json', { type: 'application/json' }));

    await waitFor(() => expect(editor().value).toBe(DASHBOARD));
    expect(screen.getByTestId('import-file-name')).toHaveTextContent('my-dashboard.json');
    expect(snackbar.error).not.toHaveBeenCalled();
  });

  // A picker set to "All files" — or a drag — reaches the handler unfiltered.
  it('refuses a file that is not JSON', async () => {
    renderModal();
    upload(new File(['title,count\n'], 'panels.csv', { type: 'text/csv' }));

    await waitFor(() => expect(snackbar.error).toHaveBeenCalledWith('Only a .json file can be uploaded.'));
    expect(editor().value).toBe('');
  });

  // Windows and some Linux desktops hand over a .json file with no MIME type.
  it('accepts a .json file that arrives with no MIME type', async () => {
    renderModal();
    upload(new File([DASHBOARD], 'no-type.json', { type: '' }));

    await waitFor(() => expect(editor().value).toBe(DASHBOARD));
    expect(snackbar.error).not.toHaveBeenCalled();
  });

  // FileReader always finishes at least a tick after the pick, and the modal
  // stays mounted between openings, so a late read could re-fill a cleared form.
  it('drops a read that lands after the modal was cancelled', async () => {
    render(<ImportDashboardModal open accountOptions={accounts} onClose={jest.fn()} onDraft={jest.fn()} />);
    upload(new File([DASHBOARD], 'stale.json', { type: 'application/json' }));
    fireEvent.click(screen.getByText('Cancel'));

    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 50));
    });
    expect(editor().value).toBe('');
    expect(screen.queryByTestId('import-file-name')).not.toBeInTheDocument();
  });

  it('refuses a file too large for the editor to hold', async () => {
    renderModal();
    const big = new File([DASHBOARD], 'huge.json', { type: 'application/json' });
    Object.defineProperty(big, 'size', { value: 3 * 1024 * 1024 });
    upload(big);

    await waitFor(() => expect(snackbar.error).toHaveBeenCalledWith('"huge.json" is larger than 2 MB. Paste the dashboard instead.'));
    expect(editor().value).toBe('');
  });
});

describe('ImportDashboardModal — import', () => {
  beforeEach(() => jest.clearAllMocks());

  // Saving here left a dashboard in the list even when the author then backed
  // out of the editor and chose Discard. The editor's Save is what creates it.
  it('hands over an unsaved draft and writes nothing', async () => {
    const onDraft = jest.fn();
    render(<ImportDashboardModal open accountOptions={accounts} onClose={jest.fn()} onDraft={onDraft} />);

    upload(new File([IMPORTABLE], 'cpu.json', { type: 'application/json' }));
    await waitFor(() => expect(screen.getByTestId('import-dashboard-btn')).toBeEnabled());
    fireEvent.click(screen.getByTestId('import-dashboard-btn'));

    expect(apiDashboards.saveDashboard).not.toHaveBeenCalled();
    expect(onDraft).toHaveBeenCalledTimes(1);
    const draft = onDraft.mock.calls[0][0];
    expect(draft.id).toBe('');
    expect(draft.title).toBe('CPU dashboard');
    expect(draft.tags).toEqual(['cpu']);
    expect(draft.definition.panels).toHaveLength(1);
    // The form is cleared for the next opening, as Cancel clears it.
    expect(editor().value).toBe('');
  });
});
