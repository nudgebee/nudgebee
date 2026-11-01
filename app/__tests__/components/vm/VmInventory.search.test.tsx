import React from 'react';
import { render, fireEvent, waitFor } from '@testing-library/react';

jest.mock('@api1/vm', () => ({
  __esModule: true,
  SEVERITY_ORDER: ['Critical', 'High', 'Medium', 'Low', 'Info'],
  default: {
    listVms: jest.fn(),
    getPackageCountsByVm: jest.fn(),
    getOsByVm: jest.fn(),
    getVulnerabilityCountsByVm: jest.fn(),
    getScanStatuses: jest.fn(),
  },
}));

jest.mock('@utils/colors');
jest.mock('@lib/auth', () => ({ hasWriteAccess: () => false }));

jest.mock('@components/vm/ScanVmDialog', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/vm/ScanAccountDialog', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/vm/VmPackages', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/vm/VmVulnerabilities', () => ({ __esModule: true, default: () => null }));
jest.mock('@shared/tables/CustomTable', () => ({ __esModule: true, default: () => null }));
jest.mock('@shared/buttons/DownloadButton', () => ({ __esModule: true, default: () => null }));
jest.mock('@shared/format/Datetime', () => ({ __esModule: true, default: () => null }));

import VmInventory from '@components/vm/VmInventory';

const apiVm = require('@api1/vm').default;

const searchBox = (container: HTMLElement) => container.querySelector('#vm-inventory-search') as HTMLInputElement;

describe('VmInventory search', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    apiVm.listVms.mockResolvedValue({ rows: [], total: 0 });
    apiVm.getPackageCountsByVm.mockResolvedValue({});
    apiVm.getOsByVm.mockResolvedValue({});
    apiVm.getVulnerabilityCountsByVm.mockResolvedValue({});
    apiVm.getScanStatuses.mockResolvedValue([]);
  });

  it('drops spaces around the typed text before it becomes the filter', async () => {
    const { container } = render(<VmInventory accountId='acc-1' />);
    await waitFor(() => expect(apiVm.listVms).toHaveBeenCalledTimes(1));

    fireEvent.change(searchBox(container), { target: { value: '  web-01  ' } });
    fireEvent.keyDown(searchBox(container), { key: 'Enter' });

    await waitFor(() => expect(apiVm.listVms).toHaveBeenCalledTimes(2));
    expect(apiVm.listVms.mock.calls.at(-1)[0].search).toBe('web-01');
  });
});
