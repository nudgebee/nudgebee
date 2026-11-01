import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';

jest.mock('@api1/vm', () => ({
  __esModule: true,
  SEVERITY_ORDER: ['Critical', 'High', 'Medium', 'Low', 'Info'],
  default: { listPackages: jest.fn() },
}));

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: { getUserPreferencesTablePageSize: jest.fn().mockReturnValue(10) },
}));

jest.mock('@utils/colors');

jest.mock('@shared/buttons/DownloadButton', () => ({ __esModule: true, default: () => null }));
jest.mock('@shared/format/Datetime', () => ({ __esModule: true, default: () => null }));
jest.mock('@ui/FilterDropdown', () => ({
  __esModule: true,
  default: ({ id, onSelect }: any) => <button data-testid={id} onClick={() => onSelect({ target: { value: 'rpm' } })} />,
}));

import VmPackages from '@components/vm/VmPackages';

const apiVm = require('@api1/vm').default;

const NEVER_SCANNED = 'No package inventory';
const NEVER_SCANNED_HINT = 'Run a scan on a VM to collect its installed packages.';
const NO_MATCH = 'No packages match these filters';
const NO_MATCH_HINT = 'Try adjusting or clearing the search, Package Type, or VM filters.';

describe('VmPackages empty state', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    apiVm.listPackages.mockResolvedValue({ rows: [], total: 0 });
  });

  it('asks for a scan when the account has no inventory and nothing is filtered', async () => {
    render(<VmPackages accountId='acc-1' />);

    expect(await screen.findByText(NEVER_SCANNED)).toBeInTheDocument();
    expect(screen.getByText(NEVER_SCANNED_HINT)).toBeInTheDocument();
    expect(screen.queryByText(NO_MATCH)).not.toBeInTheDocument();
  });

  it('says nothing matches, and how to clear it, when a search finds no package', async () => {
    const { container } = render(<VmPackages accountId='acc-1' />);
    await screen.findByText(NEVER_SCANNED);

    const searchBox = container.querySelector('#vm-packages-search') as HTMLInputElement;
    fireEvent.change(searchBox, { target: { value: 'zzqqxx' } });
    fireEvent.keyDown(searchBox, { key: 'Enter' });

    expect(await screen.findByText(NO_MATCH)).toBeInTheDocument();
    expect(screen.getByText(NO_MATCH_HINT)).toBeInTheDocument();
    expect(screen.queryByText(NEVER_SCANNED)).not.toBeInTheDocument();
    expect(screen.queryByText(NEVER_SCANNED_HINT)).not.toBeInTheDocument();
    await waitFor(() => expect(apiVm.listPackages).toHaveBeenLastCalledWith(expect.objectContaining({ search: 'zzqqxx' })));
  });

  it('says nothing matches when a filter alone empties the list', async () => {
    render(<VmPackages accountId='acc-1' />);
    await screen.findByText(NEVER_SCANNED);

    fireEvent.click(screen.getByTestId('vm-packages-type'));

    expect(await screen.findByText(NO_MATCH)).toBeInTheDocument();
    expect(screen.queryByText(NEVER_SCANNED)).not.toBeInTheDocument();
  });
});
