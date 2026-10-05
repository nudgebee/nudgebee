import '@testing-library/jest-dom';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import CloudActionModal from '@components/optimise-new/CloudActionModal';
import recommendationApi from '@api1/recommendation';
import { toast } from '@ui/Toast';

jest.mock('@api1/recommendation', () => ({
  __esModule: true,
  default: { applyRecommendation: jest.fn() },
}));

jest.mock('@ui/Toast', () => ({
  __esModule: true,
  toast: { success: jest.fn(), error: jest.fn() },
}));

const applyRecommendation = recommendationApi.applyRecommendation as jest.Mock;

const ebsRec = {
  id: 'rec-ebs',
  account_id: 'acct-1',
  rule_name: 'aws_ec2_ebs_generation_upgrade',
  status: 'Open',
  recommendation: { volume_id: 'vol-0abc', volume_region: 'us-east-1', volume_size: 500, apply_impact: 'Converts this volume to gp3 in place.' },
};

const confirmButton = () => screen.getByRole('button', { name: 'Upgrade to gp3' });

beforeEach(() => {
  applyRecommendation.mockReset();
  (toast.success as jest.Mock).mockReset();
  (toast.error as jest.Mock).mockReset();
});

describe('CloudActionModal', () => {
  it('shows what applying does and starts the upgrade with no data of its own', async () => {
    applyRecommendation.mockResolvedValue({ data: { id: 'res-1' } });
    const onSuccess = jest.fn();
    const onClose = jest.fn();
    render(<CloudActionModal open onClose={onClose} onSuccess={onSuccess} recommendation={ebsRec} />);

    expect(screen.getByText('Converts this volume to gp3 in place.')).toBeInTheDocument();
    expect(screen.getByText('vol-0abc')).toBeInTheDocument();
    fireEvent.click(confirmButton());

    await waitFor(() => expect(applyRecommendation).toHaveBeenCalledWith('acct-1', 'rec-ebs', {}));
    await waitFor(() => expect(onSuccess).toHaveBeenCalled());
    expect(onClose).toHaveBeenCalled();
    expect(toast.success).toHaveBeenCalledWith(expect.stringContaining('vol-0abc'));
  });

  it('surfaces the backend refusal instead of claiming success', async () => {
    applyRecommendation.mockResolvedValue({ errors: [{ message: 'volume vol-0abc already has a modification in progress' }] });
    const onSuccess = jest.fn();
    render(<CloudActionModal open onClose={jest.fn()} onSuccess={onSuccess} recommendation={ebsRec} />);

    fireEvent.click(confirmButton());

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('volume vol-0abc already has a modification in progress'));
    expect(onSuccess).not.toHaveBeenCalled();
  });

  it('reports a failure when the request returns neither data nor errors', async () => {
    applyRecommendation.mockResolvedValue({ data: undefined, errors: undefined });
    const onSuccess = jest.fn();
    render(<CloudActionModal open onClose={jest.fn()} onSuccess={onSuccess} recommendation={ebsRec} />);

    fireEvent.click(confirmButton());

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('Failed to upgrade to gp3'));
    expect(toast.success).not.toHaveBeenCalled();
    expect(onSuccess).not.toHaveBeenCalled();
  });

  it('says an applied upgrade is already running and does not start it again', () => {
    render(<CloudActionModal open onClose={jest.fn()} recommendation={{ ...ebsRec, status: 'InProgress' }} />);

    expect(screen.getByText('Already started')).toBeInTheDocument();
    expect(confirmButton()).toBeDisabled();
    fireEvent.click(confirmButton());
    expect(applyRecommendation).not.toHaveBeenCalled();
  });
});
