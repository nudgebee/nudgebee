import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import VmVulnerabilities from '@components/vm/VmVulnerabilities';
import apiVm from '@api1/vm';

jest.mock('@api1/vm', () => ({
  __esModule: true,
  SEVERITY_ORDER: ['Critical', 'High'],
  default: { listVulnerabilities: jest.fn(), listVulnerabilityGroups: jest.fn() },
}));
jest.mock('@hooks/useTenantBranding', () => ({ useTenantBranding: () => ({ assistantName: 'Nubi' }) }));
jest.mock('@components/recommendations/security/askNubi', () => ({
  AskNubiIconButton: () => null,
  findingPromptContext: () => '',
  useAskNubiAboutSecurityFinding: () => jest.fn(),
}));
jest.mock('@components/tickets/TicketCreatePopupForm', () => ({ __esModule: true, default: () => null }));
jest.mock('next-auth/react', () => ({ useSession: () => ({ data: null }) }));
jest.mock('next/router', () => ({ useRouter: () => ({ push: jest.fn(), query: {} }) }));
jest.mock('@ui/FilterDropdown', () => ({
  __esModule: true,
  default: (props: any) => (
    <div data-testid={props.id} data-value={props.value ?? ''} data-options={props.options.map((o: any) => o.value).join(',')}>
      {props.options.map((o: any) => (
        <button key={o.value} onClick={() => props.onSelect({ target: { value: o.value } })}>
          {props.id}:{o.value}
        </button>
      ))}
    </div>
  ),
}));

const groups = (keys: string[]) => ({
  rows: keys.map((key) => ({ key, label: key, count: 1, max_severity_weight: 1, resource_names: [] })),
  total: keys.length,
});

describe('VmVulnerabilities package options', () => {
  beforeEach(() => {
    (apiVm.listVulnerabilities as jest.Mock).mockResolvedValue({ rows: [], total: 0 });
    (apiVm.listVulnerabilityGroups as jest.Mock).mockImplementation(({ grouping, cloudResourceId }: any) => {
      if (grouping === 'vm') return Promise.resolve(groups(['vm-rhel', 'vm-ubuntu']));
      if (cloudResourceId === 'vm-rhel') return Promise.resolve(groups(['kernel', 'webkit2gtk3']));
      return Promise.resolve(groups(['linux', 'kernel', 'webkit2gtk3']));
    });
  });

  it('narrows packages to the picked VM and drops a selected package it lacks', async () => {
    render(<VmVulnerabilities accountId='acc' />);
    const pkg = await screen.findByTestId('vm-vulnerability-package');
    expect(pkg).toHaveAttribute('data-options', 'linux,kernel,webkit2gtk3');
    fireEvent.click(screen.getByText('vm-vulnerability-package:linux'));
    expect(screen.getByTestId('vm-vulnerability-package')).toHaveAttribute('data-value', 'linux');
    await act(async () => {
      fireEvent.click(screen.getByText('vm-vulnerability-vm:vm-rhel'));
    });
    await waitFor(() => expect(screen.getByTestId('vm-vulnerability-package')).toHaveAttribute('data-options', 'kernel,webkit2gtk3'));
    expect(screen.getByTestId('vm-vulnerability-package')).toHaveAttribute('data-value', '');
    expect(apiVm.listVulnerabilityGroups).toHaveBeenCalledWith(expect.objectContaining({ grouping: 'package', cloudResourceId: 'vm-rhel' }));
    expect(apiVm.listVulnerabilities).toHaveBeenLastCalledWith(expect.objectContaining({ cloudResourceId: 'vm-rhel', packageName: undefined }));
  });

  it('keeps a selected package the picked VM has', async () => {
    render(<VmVulnerabilities accountId='acc' />);
    await screen.findByTestId('vm-vulnerability-package');
    fireEvent.click(screen.getByText('vm-vulnerability-package:kernel'));
    await act(async () => {
      fireEvent.click(screen.getByText('vm-vulnerability-vm:vm-rhel'));
    });
    await waitFor(() => expect(screen.getByTestId('vm-vulnerability-package')).toHaveAttribute('data-options', 'kernel,webkit2gtk3'));
    expect(screen.getByTestId('vm-vulnerability-package')).toHaveAttribute('data-value', 'kernel');
  });
});
