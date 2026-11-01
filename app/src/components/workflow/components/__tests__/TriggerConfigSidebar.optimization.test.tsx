import React from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import TriggerConfigSidebar from '../TriggerConfigSidebar';
import recommendationApi from '@api1/recommendation';

jest.mock('@api1/recommendation', () => ({ getDistinctOptimizationRules: jest.fn() }));
jest.mock('@api1/kubernetes', () => ({}));
jest.mock('@api1/kubernetes1', () => ({}));
jest.mock('@api1/integrations', () => ({}));
jest.mock('@components/optimise-new/utils', () => ({ formatRuleName: (name: string) => name }));
jest.mock('../../hooks/useAccountOptions', () => ({
  useAccountOptions: () => ({
    options: [
      { label: 'Kubernetes', value: 'k8s', cloud_provider: 'K8s' },
      { label: 'AWS', value: 'aws', cloud_provider: 'AWS' },
    ],
    isLoading: false,
    providerOf: (id: string) => (id === 'k8s' ? 'K8s' : 'AWS'),
  }),
}));
jest.mock('../../hooks/useFilterLiveValidation', () => ({ useFilterLiveValidation: () => ({}) }));
jest.mock('../TriggerSimulatorPanel', () => () => null);
jest.mock('../ScheduleBuilder', () => () => null);
jest.mock('@shared/icons/CloudIcon', () => () => null);
jest.mock('@shared/buttons/CopyButton', () => () => null);
jest.mock('@ui/Button', () => ({
  Button: ({ children, onClick, ...props }: any) => (
    <button data-testid={props['data-testid']} onClick={onClick}>
      {children}
    </button>
  ),
}));
jest.mock('@shared/forms/FormComponents', () => ({
  FormCard: ({ children }: any) => <div>{children}</div>,
  FormField: ({ label, value, onChange, options = [] }: any) => (
    <label>
      {label}
      <select aria-label={label} value={value} onChange={onChange}>
        <option value=''>Any</option>
        {options.map((o: any) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    </label>
  ),
}));

const getRules = recommendationApi.getDistinctOptimizationRules as jest.Mock;
const node = {
  id: 'optimization',
  data: {
    trigger: {
      type: 'optimization',
      params: {
        clusters: ['k8s'],
        categories: ['RightSizing'],
        rule_names: ['pod_right_sizing'],
      },
    },
  },
};

it('reloads rules for a changed account, clears the saved rule, and ignores a stale account response', async () => {
  let resolveK8s!: (rows: any[]) => void;
  let resolveAWS!: (rows: any[]) => void;
  getRules.mockImplementation((accountId) => {
    if (accountId === 'k8s')
      return new Promise((resolve) => {
        resolveK8s = resolve;
      });
    if (accountId === 'aws')
      return new Promise((resolve) => {
        resolveAWS = resolve;
      });
    return Promise.resolve([]);
  });
  render(<TriggerConfigSidebar open selectedNode={node as any} onClose={jest.fn()} onSave={jest.fn()} accountId='k8s' workflowData={{}} />);
  await waitFor(() => expect(getRules).toHaveBeenCalledWith('k8s'));

  fireEvent.change(screen.getByLabelText('Cluster'), { target: { value: 'aws' } });
  await waitFor(() => expect(getRules).toHaveBeenCalledWith('aws'));
  expect(screen.getByLabelText('Rule Name')).toHaveValue('');

  await act(async () => resolveAWS([{ category: 'RightSizing', rule_name: 'aws_idle_instance' }]));
  expect(screen.getByRole('option', { name: 'aws_idle_instance' })).toBeInTheDocument();
  await act(async () => resolveK8s([{ category: 'RightSizing', rule_name: 'pod_right_sizing' }]));
  expect(screen.queryByRole('option', { name: 'pod_right_sizing' })).not.toBeInTheDocument();
  expect(screen.getByRole('option', { name: 'aws_idle_instance' })).toBeInTheDocument();
});
