import React, { useState } from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import type { Node } from 'reactflow';
import TriggerConfigSidebar from '../TriggerConfigSidebar';

jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: { getEventFilterValues: jest.fn().mockResolvedValue({ data: { filters: [] } }) },
}));
jest.mock('@api1/kubernetes1', () => ({
  __esModule: true,
  default: { getAllEventRuleNames: jest.fn().mockResolvedValue({ data: { event_rules: [] } }) },
}));
jest.mock('@api1/integrations', () => ({ __esModule: true, default: {} }));
jest.mock('@api1/recommendation', () => ({ __esModule: true, default: {} }));
jest.mock('@components/optimise-new/utils', () => ({ formatRuleName: (v: string) => v }));
jest.mock('../../hooks/useAccountOptions', () => ({
  useAccountOptions: () => ({ options: [], isLoading: false, providerOf: () => '' }),
}));
jest.mock('../../hooks/useFilterLiveValidation', () => ({ useFilterLiveValidation: () => ({ error: '' }) }));
jest.mock('../TriggerSimulatorPanel', () => () => null);
jest.mock('@lib/auth', () => ({ getSessionAccountIds: () => [], hasAnyCustomGrant: () => true, isTenantWideRole: () => true }));

const eventNode: Node = { id: 't1', type: 'trigger', position: { x: 0, y: 0 }, data: { trigger: { type: 'event', params: { filter: '' } } } };

// Mirrors the builder: Save writes the config onto the selected node (updateTriggerConfig).
const Host = () => {
  const [node, setNode] = useState<Node>(eventNode);
  return (
    <TriggerConfigSidebar
      open
      selectedNode={node}
      onClose={() => {}}
      onSave={(_id, config) => setNode((n) => ({ ...n, data: { ...n.data, trigger: { ...config, valid: true, errors: [] } } }))}
      accountId='acct-1'
      workflowData={{ id: 'wf-1' }}
    />
  );
};

const expressionBox = () => document.querySelector('textarea:not([aria-hidden])') as HTMLTextAreaElement;

// #40526 review: Save hands the sidebar a new selectedNode; that must not re-seed the form.
describe('TriggerConfigSidebar save', () => {
  it('stays in advanced mode after saving an expression the pickers could show', () => {
    render(<Host />);
    fireEvent.click(screen.getByText('Switch to advanced filter expression'));
    fireEvent.change(expressionBox(), { target: { value: '{{ event.priority == "HIGH" }}' } });
    fireEvent.click(screen.getByTestId('trigger-sidebar-save-btn'));

    expect(expressionBox()).toHaveValue('{{ event.priority == "HIGH" }}');
    expect(screen.getByText('Switch to structured filters')).toBeInTheDocument();
    expect(screen.queryByTestId('trigger-sidebar-save-btn')).not.toBeInTheDocument();
  });
});
