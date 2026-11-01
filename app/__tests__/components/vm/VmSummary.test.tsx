import React from 'react';
import { render, waitFor } from '@testing-library/react';

jest.mock('@api1/vm', () => ({
  __esModule: true,
  SEVERITY_ORDER: ['Critical', 'High', 'Medium', 'Low', 'Info'],
  default: {
    listVms: jest.fn(),
    getPackageCountsByVm: jest.fn(),
    listPackages: jest.fn(),
    getSeverityCounts: jest.fn(),
    listAgents: jest.fn(),
    listVulnerabilityGroups: jest.fn(),
    getOsByVm: jest.fn(),
  },
}));

jest.mock('@utils/colors');
jest.mock('@shared/format/Datetime', () => ({ __esModule: true, default: () => null }));

import VmSummary from '@components/vm/VmSummary';

const apiVm = require('@api1/vm').default;

// The fleet from #39845: seven VMs whose installs add up to 3,652, holding
// 1,617 distinct packages between them.
const INSTALLS_PER_VM = [494, 435, 495, 619, 613, 497, 499];
const DISTINCT_PACKAGES = 1617;

describe('VmSummary', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    apiVm.listVms.mockResolvedValue({ rows: [], total: INSTALLS_PER_VM.length });
    apiVm.getPackageCountsByVm.mockResolvedValue(
      Object.fromEntries(INSTALLS_PER_VM.map((count, i) => [`vm-${i}`, { count, lastSeenAt: '2026-10-01T00:00:00Z' }]))
    );
    apiVm.listPackages.mockResolvedValue({ rows: [], total: DISTINCT_PACKAGES });
    apiVm.getSeverityCounts.mockResolvedValue({});
    apiVm.listAgents.mockResolvedValue([]);
    apiVm.listVulnerabilityGroups.mockResolvedValue({ rows: [] });
    apiVm.getOsByVm.mockResolvedValue({});
  });

  it('shows the same package total as the Packages tab, not the sum of per-VM installs', async () => {
    const { container } = render(<VmSummary accountId='acc-1' />);
    const tile = () => container.querySelector('#vm-summary-packages');

    await waitFor(() => expect(tile()).toHaveTextContent('Packages Tracked1,617'));
    expect(tile()).not.toHaveTextContent('3,652');
    // Unfiltered, like the tab's first load, so the two totals are one number.
    expect(apiVm.listPackages).toHaveBeenCalledWith({ accountId: 'acc-1', limit: 1, offset: 0 });
  });

  it('still counts inventoried VMs from the per-VM package counts', async () => {
    const { container } = render(<VmSummary accountId='acc-1' />);

    await waitFor(() => expect(container.querySelector('#vm-summary-scanned')).toHaveTextContent('Inventoried7'));
  });
});
