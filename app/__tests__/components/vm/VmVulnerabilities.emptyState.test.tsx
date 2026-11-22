import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';

jest.mock('@api1/vm', () => ({
  __esModule: true,
  SEVERITY_ORDER: ['Critical', 'High', 'Medium', 'Low', 'Info'],
  default: { listVulnerabilities: jest.fn(), listVulnerabilityGroups: jest.fn() },
}));

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: { getUserPreferencesTablePageSize: jest.fn().mockReturnValue(10) },
}));

jest.mock('@utils/colors');
jest.mock('@lib/auth', () => ({ canCreateTicket: () => false, hasWriteAccess: () => false }));
jest.mock('@hooks/useTenantBranding', () => ({ useTenantBranding: () => ({ assistantName: 'Nubi' }) }));
jest.mock('@components/recommendations/security/askNubi', () => ({
  AskNubiIconButton: () => null,
  findingPromptContext: () => '',
  useAskNubiAboutSecurityFinding: () => jest.fn(),
}));
jest.mock('@components/recommendations/security/dismissal', () => ({
  dismissMenuEntry: () => ({}),
  useRecommendationDismissal: () => ({ toggle: jest.fn(), modal: null }),
}));
jest.mock('@components/recommendations/security/SecurityFindingPanel', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/tickets/TicketCreatePopupForm', () => ({ __esModule: true, default: () => null }));
jest.mock('@shared/buttons/DownloadButton', () => ({ __esModule: true, default: () => null }));
jest.mock('@shared/format/Datetime', () => ({ __esModule: true, default: () => null }));
jest.mock('@ui/FilterDropdown', () => ({
  __esModule: true,
  default: ({ id, onSelect }: any) => <button data-testid={id} onClick={() => onSelect({ target: { value: 'Critical' } })} />,
}));

import VmVulnerabilities from '@components/vm/VmVulnerabilities';

const apiVm = require('@api1/vm').default;

const NONE = 'No open vulnerabilities';
const NONE_HINT = 'Findings appear here after a VM package scan matches installed packages against the CVE database.';
const NO_MATCH = 'No vulnerabilities match these filters';
const NO_MATCH_HINT = 'Try adjusting or clearing the Severity, Status, VM, or Package filters.';

describe('VmVulnerabilities empty state', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    apiVm.listVulnerabilities.mockResolvedValue({ rows: [], total: 0 });
    apiVm.listVulnerabilityGroups.mockResolvedValue({ rows: [], total: 0 });
  });

  it('points an expanded VM row with no findings at a scan, not at filters it does not show', async () => {
    render(<VmVulnerabilities accountId='acc-1' cloudResourceId='vm-1' embedded />);

    expect(await screen.findByText(NONE)).toBeInTheDocument();
    expect(screen.getByText(NONE_HINT)).toBeInTheDocument();
    expect(screen.queryByText(NO_MATCH)).not.toBeInTheDocument();
    expect(screen.queryByText(NO_MATCH_HINT)).not.toBeInTheDocument();
    expect(apiVm.listVulnerabilities).toHaveBeenCalledWith(expect.objectContaining({ cloudResourceId: 'vm-1' }));
  });

  it('still says nothing matches when the top-level view is narrowed by its own filter', async () => {
    render(<VmVulnerabilities accountId='acc-1' />);
    await screen.findByText(NONE);

    fireEvent.click(screen.getByTestId('vm-vulnerability-severity'));

    expect(await screen.findByText(NO_MATCH)).toBeInTheDocument();
    expect(screen.getByText(NO_MATCH_HINT)).toBeInTheDocument();
    await waitFor(() => expect(apiVm.listVulnerabilities).toHaveBeenLastCalledWith(expect.objectContaining({ severity: 'Critical' })));
  });
});
