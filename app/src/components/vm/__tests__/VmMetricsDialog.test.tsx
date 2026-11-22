import { render, screen } from '@testing-library/react';
import VmMetricsDialog from '@components/vm/VmMetricsDialog';

jest.mock('@components/vm/VmMetrics', () => ({
  __esModule: true,
  default: ({ vm }: { vm: { id: string } }) => <div data-testid='vm-metrics'>history for {vm.id}</div>,
}));

const vm = { id: 'res-1', name: 'web-1', resourse_id: 'i-0abc', meta: { private_ip: '10.0.0.5' } } as any;

describe('VmMetricsDialog', () => {
  it('shows the VM history with its name and address when a VM is set', () => {
    render(<VmMetricsDialog accountId='acc-1' vm={vm} onClose={jest.fn()} />);
    expect(screen.getByText('web-1 metrics')).toBeInTheDocument();
    expect(screen.getByText('10.0.0.5')).toBeInTheDocument();
    expect(screen.getByTestId('vm-metrics')).toHaveTextContent('history for res-1');
  });

  it('renders nothing while no VM is selected', () => {
    render(<VmMetricsDialog accountId='acc-1' vm={null} onClose={jest.fn()} />);
    expect(screen.queryByTestId('vm-metrics')).not.toBeInTheDocument();
    expect(screen.queryByText(/metrics$/)).not.toBeInTheDocument();
  });
});
