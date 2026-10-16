import { render, screen } from '@testing-library/react';
import AutomationToolsList from '@components/llm/AutomationToolsList';
import { queryGraphQL } from '@lib/HttpService';

jest.mock('@lib/HttpService', () => ({ queryGraphQL: jest.fn() }));
jest.mock('@api1/tool-approval', () => ({ listToolApprovalAccounts: jest.fn(() => Promise.resolve([{ id: 'acct-1', account_name: 'Prod' }])) }));
jest.mock('@shared/tables/CustomTable', () => ({
  __esModule: true,
  default: ({ tableData }) => (
    <div>
      {tableData.map((row, i) => (
        <div key={i} data-testid='row'>
          {row.map((cell, j) => (
            <span key={j}>{cell.component ?? cell.text}</span>
          ))}
        </div>
      ))}
    </div>
  ),
}));

const page = (workflows, next) => ({ data: { data: { workflow_list: { workflows, next_page_token: next } } } });

// The Automations sub-tab lists automations Nubi can run and whether they ask (#40399).
describe('AutomationToolsList', () => {
  it('lists only active, published automations Nubi can run, across pages, with whether each asks', async () => {
    queryGraphQL
      .mockResolvedValueOnce(
        page(
          [
            {
              id: 'w1',
              name: 'Restart api',
              account_id: 'acct-1',
              status: 'ACTIVE',
              live_version_id: 'v1',
              ai_invocable: true,
              ai_auto_approve: false,
              definition: { llm_description: 'Restarts the api deployment' },
            },
          ],
          'next'
        )
      )
      .mockResolvedValueOnce(
        page([
          {
            id: 'w2',
            name: 'Collect logs',
            account_id: 'acct-1',
            status: 'ACTIVE',
            live_version_id: 'v2',
            ai_invocable: true,
            ai_auto_approve: true,
            definition: { llm_description: 'Collects pod logs' },
          },
          { id: 'w3', name: 'Not for Nubi', account_id: 'acct-1', status: 'ACTIVE', live_version_id: 'v3', ai_invocable: false },
          { id: 'w4', name: 'Paused one', account_id: 'acct-1', status: 'PAUSED', live_version_id: 'v4', ai_invocable: true },
          { id: 'w5', name: 'Never published', account_id: 'acct-1', status: 'ACTIVE', ai_invocable: true },
          {
            id: 'w6',
            name: 'Undescribed',
            account_id: 'acct-1',
            status: 'ACTIVE',
            live_version_id: 'v6',
            ai_invocable: true,
            definition: { llm_description: ' ' },
          },
          {
            id: 'w7',
            name: 'Agent trigger',
            account_id: 'acct-1',
            status: 'ACTIVE',
            live_version_id: 'v7',
            ai_invocable: true,
            bee: { bee: 'b1', role: 'trigger' },
            definition: { llm_description: 'Runs for an agent' },
          },
        ])
      );
    render(<AutomationToolsList />);

    expect(await screen.findByText('Restart api')).toBeInTheDocument();
    expect(screen.getByText('Collect logs')).toBeInTheDocument();
    expect(screen.queryByText('Not for Nubi')).not.toBeInTheDocument();
    expect(screen.queryByText('Paused one')).not.toBeInTheDocument();
    expect(screen.queryByText('Never published')).not.toBeInTheDocument();
    expect(screen.queryByText('Undescribed')).not.toBeInTheDocument();
    expect(screen.queryByText('Agent trigger')).not.toBeInTheDocument();
    expect(screen.getByText('Always asks')).toBeInTheDocument();
    expect(screen.getByText('Never asks')).toBeInTheDocument();
    expect(screen.getAllByText('Prod')).toHaveLength(2);
    expect(queryGraphQL).toHaveBeenCalledTimes(2);
  });
});
