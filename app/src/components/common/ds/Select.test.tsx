import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { Select, visibleChipCount } from './Select';

// #38137: the trigger used to show a fixed two labels whatever their length, so
// a panel scoped to two long account names read "iteration-prod-cl…,
// nudgebee-bill…" — naming neither. The count now follows the text.
describe('visibleChipCount', () => {
  it('shows both when two short labels fit the budget', () => {
    expect(visibleChipCount(['aws', 'gcp'], 2)).toBe(2);
  });

  it('drops to one when the second label would not fit', () => {
    expect(visibleChipCount(['iteration-prod-cluster (kubernetes)', 'nudgebee-billing-prod (aws)'], 2)).toBe(1);
  });

  it('always shows the first label, however long — "+5" alone names nothing', () => {
    expect(visibleChipCount(['a-single-account-name-far-past-any-sensible-budget'], 2)).toBe(1);
  });

  it('treats maxChips as a ceiling the budget can only lower', () => {
    expect(visibleChipCount(['aws', 'gcp', 'azure'], 2)).toBe(2);
    expect(visibleChipCount(['aws', 'gcp', 'azure'], 4)).toBe(3);
    expect(visibleChipCount(['ap-southeast-1', 'us-east-1', 'eu-central-1'], 4)).toBe(2);
  });

  it('shows one for a nonsensical ceiling rather than none', () => {
    expect(visibleChipCount(['aws', 'gcp'], 0)).toBe(1);
  });

  it('shows nothing when nothing is selected', () => {
    expect(visibleChipCount([], 2)).toBe(0);
  });
});

describe('Select trigger with long selections', () => {
  const longAccounts = [
    { value: 'a', label: 'iteration-prod-cluster (kubernetes)' },
    { value: 'b', label: 'nudgebee-billing-prod (aws)' },
    { value: 'c', label: 'nudgebee-analytics-prod (gcp)' },
  ];

  it('names one account and counts the rest', () => {
    render(<Select multiple value={['a', 'b', 'c']} options={longAccounts} onChange={() => {}} label='Accounts' />);

    expect(screen.getByText('iteration-prod-cluster (kubernetes)')).toBeInTheDocument();
    expect(screen.queryByText('nudgebee-billing-prod (aws)')).not.toBeInTheDocument();
    expect(screen.getByText('+2')).toBeInTheDocument();
  });

  it('keeps both short selections inline', () => {
    render(
      <Select
        multiple
        value={['a', 'b']}
        options={[
          { value: 'a', label: 'aws' },
          { value: 'b', label: 'gcp' },
        ]}
        onChange={() => {}}
        label='Providers'
      />
    );

    expect(screen.getByText('aws')).toBeInTheDocument();
    expect(screen.getByText('gcp')).toBeInTheDocument();
    expect(screen.queryByText(/^\+\d+$/)).not.toBeInTheDocument();
  });
});

// Grouped mode ships collapsed, which suits a filter pill but not a picker whose
// rows are the reason you opened it — the panel editor's Accounts field would
// ask for a click before showing a single account. `defaultGroupsOpen` flips it.
describe('Select grouped mode', () => {
  const accounts = [
    { value: 'w1', label: 'aws-demo', group: 'AWS' },
    { value: 'k1', label: 'k8s-prod', group: 'K8S' },
    { value: 'k2', label: 'sandbox cluster', group: 'K8S' },
  ];

  const openPicker = (extra: Record<string, unknown> = {}) => {
    render(<Select multiple grouped value={[]} options={accounts} onChange={() => {}} label='Accounts' {...extra} />);
    fireEvent.click(screen.getByLabelText('Accounts', { selector: 'button' }));
  };

  it('keeps groups shut by default', () => {
    openPicker();

    expect(screen.getByText('AWS')).toBeInTheDocument();
    expect(screen.getByText('K8S')).toBeInTheDocument();
    expect(screen.queryByText('aws-demo')).not.toBeInTheDocument();
  });

  it('shows every row up front with defaultGroupsOpen', () => {
    openPicker({ defaultGroupsOpen: true });

    expect(screen.getByText('aws-demo')).toBeInTheDocument();
    expect(screen.getByText('k8s-prod')).toBeInTheDocument();
    expect(screen.getByText('sandbox cluster')).toBeInTheDocument();
  });

  it('still collapses on the first header click when it started open', () => {
    openPicker({ defaultGroupsOpen: true });

    fireEvent.click(screen.getByText('K8S'));

    expect(screen.queryByText('k8s-prod')).not.toBeInTheDocument();
    expect(screen.getByText('aws-demo')).toBeInTheDocument();
  });

  it('falls back to a flat list when every account is one provider', () => {
    render(
      <Select
        multiple
        grouped
        defaultGroupsOpen
        value={[]}
        options={[
          { value: 'k1', label: 'k8s-prod', group: 'K8S' },
          { value: 'k2', label: 'k8s-dev', group: 'K8S' },
        ]}
        onChange={() => {}}
        label='Accounts'
      />
    );
    fireEvent.click(screen.getByLabelText('Accounts', { selector: 'button' }));

    expect(screen.queryByText('K8S')).not.toBeInTheDocument();
    expect(screen.getByText('k8s-prod')).toBeInTheDocument();
  });
});
