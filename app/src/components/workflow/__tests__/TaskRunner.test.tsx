import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import TaskRunner from '../TaskRunner';

jest.mock('@api1/workflow', () => ({
  __esModule: true,
  default: { listTaskDefinitions: jest.fn() },
}));

jest.mock('@api1/home', () => ({
  __esModule: true,
  default: { getCloudAccounts: jest.fn(() => Promise.resolve([])) },
}));

jest.mock('@lib/auth', () => ({ hasWriteAccess: () => true }));

// The real sidebar is a large builder panel; only the title it would get matters here.
jest.mock('../ActionDetailsSidebar', () => ({
  __esModule: true,
  default: ({ actionLabel }: { actionLabel?: string }) => <div data-testid='sidebar-title'>{`Action Details - ${actionLabel}`}</div>,
}));

import apiWorkflow from '@api1/workflow';

const mockList = (apiWorkflow as any).listTaskDefinitions as jest.Mock;

describe('TaskRunner', () => {
  it('titles the action panel with the task list label, not the description', async () => {
    mockList.mockResolvedValue({
      data: { workflow_list_taskdefinitions: { tasks: [{ name: 'network.dns', description: 'Look up DNS records for a domain.' }] } },
    });
    render(<TaskRunner />);

    fireEvent.click(await screen.findByText('Networking'));
    fireEvent.click(await screen.findByText('DNS'));

    expect(screen.getByTestId('sidebar-title')).toHaveTextContent('Action Details - DNS');
    expect(screen.getByTestId('sidebar-title')).not.toHaveTextContent('Look up DNS records');
  });
});
