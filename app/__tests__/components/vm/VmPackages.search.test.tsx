import React from 'react';
import { render, fireEvent, waitFor } from '@testing-library/react';

jest.mock('@api1/vm', () => ({
  __esModule: true,
  SEVERITY_ORDER: ['Critical', 'High', 'Medium', 'Low', 'Info'],
  default: { listPackages: jest.fn() },
}));

jest.mock('@utils/colors');

jest.mock('@shared/tables/CustomTable', () => ({
  __esModule: true,
  default: ({ totalRows }: any) => <div data-testid='custom-table'>{totalRows}</div>,
}));

jest.mock('@shared/buttons/DownloadButton', () => ({ __esModule: true, default: () => null }));
jest.mock('@shared/format/Datetime', () => ({ __esModule: true, default: () => null }));
jest.mock('@ui/FilterDropdown', () => ({ __esModule: true, default: () => null }));

import VmPackages from '@components/vm/VmPackages';

const apiVm = require('@api1/vm').default;

const searchBox = (container: HTMLElement) => container.querySelector('#vm-packages-search') as HTMLInputElement;

const lastSearch = () => apiVm.listPackages.mock.calls.at(-1)[0].search;

describe('VmPackages search', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    apiVm.listPackages.mockResolvedValue({ rows: [], total: 0 });
  });

  it('drops spaces around the typed text before it becomes the filter', async () => {
    const { container } = render(<VmPackages accountId='acc-1' />);
    await waitFor(() => expect(apiVm.listPackages).toHaveBeenCalledTimes(1));

    fireEvent.change(searchBox(container), { target: { value: '  openssl  ' } });
    fireEvent.keyDown(searchBox(container), { key: 'Enter' });

    await waitFor(() => expect(apiVm.listPackages).toHaveBeenCalledTimes(2));
    expect(lastSearch()).toBe('openssl');
  });

  it('keeps a space inside the text', async () => {
    const { container } = render(<VmPackages accountId='acc-1' />);
    await waitFor(() => expect(apiVm.listPackages).toHaveBeenCalledTimes(1));

    fireEvent.change(searchBox(container), { target: { value: ' linux image ' } });
    fireEvent.keyDown(searchBox(container), { key: 'Enter' });

    await waitFor(() => expect(apiVm.listPackages).toHaveBeenCalledTimes(2));
    expect(lastSearch()).toBe('linux image');
  });
});
